package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
	"lazymind/core/state"
	"lazymind/core/store"
)

func TestChatFallbackPreparesAfterFixedModelDeletion(t *testing.T) {
	db := newChatExecutionTestDB(t).DB
	seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "A", "gpt-a", "llm", true, "fixture-a")
	seedAvailableChatModel(t, db, "user-1", "provider-b", "group-b", "model-b", "OpenAI", "B", "gpt-b", "llm", true, "fixture-b")
	conversation, _, err := ensureConversation(context.Background(), db, "fallback-deleted", "Fixed", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: "fixed", ModelID: "model-a", Source: "own"})
	if err != nil {
		t.Fatal(err)
	}
	before := string(conversation.ChatModelSnapshot)
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-a").Update("deleted_at", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
	if err := applyConversationChatModelConfig(context.Background(), db, "user-1", body); err != nil {
		t.Fatalf("a deleted initial model must permit an authorized replacement: %v", err)
	}
	if got := body["llm_config"].(map[string]any)["llm"].(map[string]any)["model"]; got != "gpt-b" {
		t.Fatalf("prepared model = %v", got)
	}
	var stored orm.Conversation
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if string(stored.ChatModelSnapshot) != before {
		t.Fatal("preparation changed the fixed snapshot")
	}
}

func TestChatFallbackDoesNotDecryptBrokenDefaultBeforeAlternatives(t *testing.T) {
	db := newChatExecutionTestDB(t).DB
	seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "A", "gpt-a", "llm", true, "fixture-a")
	seedAvailableChatModel(t, db, "user-1", "provider-b", "group-b", "model-b", "OpenAI", "B", "gpt-b", "llm", true, "fixture-b")
	seedSelectedChatModel(t, db, "user-1", "model-a", false)
	conversation, _, err := ensureConversation(context.Background(), db, "fallback-key", "Fixed", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: "fixed", ModelID: "model-a", Source: "own"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Update("api_key_ciphertext", "fixture-invalid-envelope").Error; err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
	if err := applyChatRuntimeConfigs(context.Background(), db, "user-1", body); err != nil {
		t.Fatalf("a broken default chat credential must not block alternatives: %v", err)
	}
	if got := body["llm_config"].(map[string]any)["llm"].(map[string]any)["model"]; got != "gpt-b" {
		t.Fatalf("prepared model = %v", got)
	}
}

func fallbackTestBody(t *testing.T, mode string) (*gorm.DB, *orm.Conversation, map[string]any) {
	t.Helper()
	db := newChatExecutionTestDB(t).DB
	seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "A", "gpt-a", "llm", true, "fixture-a")
	seedAvailableChatModel(t, db, "user-1", "provider-b", "group-b", "model-b", "OpenAI", "B", "gpt-b", "llm", true, "fixture-b")
	seedSelectedChatModel(t, db, "user-1", "model-a", false)
	selection := &initialChatModelSelection{Mode: mode}
	if mode == "fixed" {
		selection.ModelID = "model-a"
		selection.Source = "own"
	}
	conversation, _, err := ensureConversation(t.Context(), db, "fallback-test", "Fallback", nil, nil, "user-1", "User", false, "", nil, selection)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1", "query": "hello", "run_id": "run-fallback"}
	if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
		t.Fatal(err)
	}
	return db, conversation, body
}
func fallbackWireEvent(run, id, kind string, data any) *ChatRuntimeEvent {
	raw, _ := json.Marshal(data)
	return &ChatRuntimeEvent{SchemaVersion: 1, RunID: run, EventID: id, Type: kind, Data: raw}
}
func fallbackSwitchEvent(body map[string]any, run string) *ChatRuntimeEvent {
	state := body[chatModelFallbackBodyKey].(*chatFallbackState)
	return fallbackWireEvent(run, "switch-b", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: 1, From: state.identity("own:model-a"), To: state.identity("own:model-b"), ReasonCode: "not_found"})
}
func fallbackFinishedEvent(run, call, finish string) *ChatRuntimeEvent {
	return fallbackWireEvent(run, "finished-"+call, RuntimeEventModelCallFinished, map[string]any{"model_call_id": call, "attempt_count": 1, "kind": "finish", "has_semantic_output": true, "finish": finish})
}

func TestChatFallbackAcceptsOnlyConfirmedAuthorizedModel(t *testing.T) {
	_, _, body := fallbackTestBody(t, "fixed")
	ext := mergeChatModelRouteIntoExt(nil, body)
	switched := fallbackSwitchEvent(body, "run-fallback")
	var contaminated map[string]any
	_ = json.Unmarshal(switched.Data, &contaminated)
	contaminated["api_key"] = "fixture-secret-canary"
	contaminated["to"].(map[string]any)["model_name"] = "untrusted-name"
	switched.Data, _ = json.Marshal(contaminated)
	accepted, err := acceptChatFallbackEvent(body, switched, "run-fallback", &ext)
	if err != nil || !accepted {
		t.Fatalf("accept Host success: %t %v", accepted, err)
	}
	if chatModelRouteFromBody(body).ModelID != "model-b" || chatModelRouteFromHistoryExt(ext).ModelID != "model-b" {
		t.Fatal("confirmed route was not projected")
	}
	if strings.Contains(string(switched.Data), "fixture-secret-canary") || strings.Contains(string(switched.Data), "untrusted-name") {
		t.Fatal("untrusted fields escaped typed sanitization")
	}
	if records := modelSwitchesFromExt(ext); len(records) != 1 || records[0].To.ModelName != "gpt-b" {
		t.Fatalf("records=%#v", records)
	}
	if accepted, err := acceptChatFallbackEvent(body, switched, "run-fallback", &ext); err != nil || accepted {
		t.Fatalf("duplicate success: %t %v", accepted, err)
	}
	var conflict ModelSwitchedData
	_ = json.Unmarshal(switched.Data, &conflict)
	conflict.ReasonCode = "rate_limited"
	switched.Data, _ = json.Marshal(conflict)
	if _, err := acceptChatFallbackEvent(body, switched, "run-fallback", &ext); err == nil {
		t.Fatal("conflicting event ID was accepted")
	}
}

func TestChatFallbackRejectsWrongRunCandidateAndBootstrapForgery(t *testing.T) {
	for _, kind := range []string{"run", "candidate", "bootstrap"} {
		t.Run(kind, func(t *testing.T) {
			_, _, body := fallbackTestBody(t, "auto")
			var ext json.RawMessage
			event := fallbackSwitchEvent(body, "run-fallback")
			switch kind {
			case "run":
				event.RunID = "stale-run"
			case "candidate":
				var data ModelSwitchedData
				_ = json.Unmarshal(event.Data, &data)
				data.To.ModelID = "private-model"
				data.To.CandidateID = "own:private-model"
				event.Data, _ = json.Marshal(data)
			case "bootstrap":
				var data ModelSwitchedData
				_ = json.Unmarshal(event.Data, &data)
				data.SwitchIndex = 0
				event.Data, _ = json.Marshal(data)
			}
			if _, err := acceptChatFallbackEvent(body, event, "run-fallback", &ext); err == nil {
				t.Fatal("invalid replacement was accepted")
			}
			if chatModelRouteFromBody(body).ModelID != "model-a" || len(modelSwitchesFromBody(body)) != 0 {
				t.Fatal("rejected event changed projection")
			}
		})
	}
}
func TestChatFallbackSDKOutcomeDoesNotCommitModel(t *testing.T) {
	_, _, body := fallbackTestBody(t, "fixed")
	var ext json.RawMessage
	for _, finish := range []string{"length", "stop"} {
		if _, err := acceptChatFallbackEvent(body, fallbackFinishedEvent("run-fallback", "call-b", finish), "run-fallback", &ext); err != nil {
			t.Fatal(err)
		}
	}
	if chatModelRouteFromBody(body).ModelID != "model-a" || len(modelSwitchesFromBody(body)) != 0 {
		t.Fatal("SDK observation committed a replacement")
	}
}

func TestChatFallbackHistoryAndSnapshotAcrossSingleReplyModes(t *testing.T) {
	for _, mode := range []string{"fixed", "auto"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", mode, stream), func(t *testing.T) {
				db, conversation, body := fallbackTestBody(t, mode)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request LazyChatRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					if request.Runtime.ModelFallbackCurrentID != "own:model-a" || len(request.Runtime.ModelFallbackCandidates) != 2 {
						t.Errorf("private candidate wire is incomplete")
					}
					encoder := json.NewEncoder(w)
					emit := func(data map[string]any) { _ = encoder.Encode(map[string]any{"code": 200, "data": data}) }
					emit(map[string]any{"runtime_event": fallbackSwitchEvent(body, request.Conversation.RunID)})
					emit(map[string]any{"text": "answer"})
					invoked := false
					emit(map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true, ModelInvoked: &invoked})})
				}))
				defer server.Close()
				ext := mergeChatModelRouteIntoExt(nil, body)
				recorder := httptest.NewRecorder()
				target := chatPersistTarget{HistoryID: "fallback-history", Seq: 1}
				if stream {
					streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, ext)
				} else {
					handleNonStreamChat(recorder, t.Context(), db, nil, server.URL, body, conversation.ID, "hello", target, ext)
				}
				var history orm.ChatHistory
				if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
					t.Fatalf("history: %v response=%s", err, recorder.Body.String())
				}
				if history.RunStatus != "completed" || chatModelRouteFromHistoryExt(history.Ext).ModelID != "model-b" || len(modelSwitchesFromExt(history.Ext)) != 1 {
					t.Fatalf("wrong durable outcome: status=%s ext=%s response=%s", history.RunStatus, history.Ext, recorder.Body.String())
				}
				if strings.Contains(string(history.Ext), "fixture-a") || strings.Contains(string(history.Ext), "fixture-b") || strings.Contains(recorder.Body.String(), "fixture-a") || strings.Contains(recorder.Body.String(), "fixture-b") {
					t.Fatal("private credentials entered a public projection")
				}
				var stored orm.Conversation
				if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
					t.Fatal(err)
				}
				if mode == "fixed" {
					if stored.ChatModelID == nil || *stored.ChatModelID != "model-b" || stored.ChatModelSource == nil || *stored.ChatModelSource != "own" {
						t.Fatal("successful replacement did not update the conversation selection")
					}
				} else {
					var snapshot chatModelSnapshot
					_ = json.Unmarshal(stored.ChatModelSnapshot, &snapshot)
					if snapshot.ModelID != "model-b" || snapshot.SuccessfulRunID != history.RunID {
						t.Fatalf("Auto did not save actual successful model: %s", stored.ChatModelSnapshot)
					}
				}
				item := chatHistoryToResponseItem(history)
				if len(item["model_switches"].([]ModelSwitchRecord)) != 1 {
					t.Fatal("REST history omitted replacement facts")
				}
			})
		}
	}
}

type fallbackSelectionRecorder struct {
	*httptest.ResponseRecorder
	onSwitch func()
}

func (recorder *fallbackSelectionRecorder) Write(data []byte) (int, error) {
	if strings.Contains(string(data), `"type":"model_switched"`) {
		recorder.onSwitch()
	}
	return recorder.ResponseRecorder.Write(data)
}

func TestChatFallbackSelectionIsSavedBeforeSuccessAndSurvivesLaterFailure(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": fallbackSwitchEvent(body, request.Conversation.RunID)}})
		_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "failed", Reason: "model_failure", Code: "model_fallback_exhausted"})}})
	}))
	defer server.Close()
	checked := false
	recorder := &fallbackSelectionRecorder{ResponseRecorder: httptest.NewRecorder(), onSwitch: func() {
		checked = true
		var stored orm.Conversation
		if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
			t.Error(err)
			return
		}
		selection := resolvedSelectionForConversation(&stored, nil, nil)
		if selection.ModelID != "model-b" || selection.Source != "own" {
			t.Errorf("selection at published success = %#v, want B", selection)
		}
		next := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
		if err := applyConversationChatModelConfig(t.Context(), db, "user-1", next); err != nil || chatModelRouteFromBody(next).ModelID != "model-b" {
			t.Errorf("next request did not use B: %v", err)
		}
	}}
	target := chatPersistTarget{HistoryID: "selection-sync-history", Seq: 1}
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
	var stored orm.Conversation
	_ = db.Where("id = ?", conversation.ID).Take(&stored).Error
	if !checked || stored.ChatModelID == nil || *stored.ChatModelID != "model-b" {
		t.Fatal("confirmed B was lost when the overall run failed")
	}
	var defaults []orm.UserSelectedModel
	if err := db.Where("user_id = ?", "user-1").Find(&defaults).Error; err != nil {
		t.Fatal(err)
	}
	for _, selection := range defaults {
		if selection.UserModelProviderGroupModelID != "model-a" {
			t.Fatal("conversation fallback changed the account default")
		}
	}
}

func TestChatFallbackSelectionPersistenceGuards(t *testing.T) {
	for _, scenario := range []string{"manual-selection", "new-turn", "regenerated", "write-failure", "other-conversation"} {
		t.Run(scenario, func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, "fixed")
			target := chatPersistTarget{HistoryID: "selection-history", Seq: 1}
			ext := mergeChatModelRouteIntoExt(nil, body)
			if err := registerForkableHistoryRun(t.Context(), db, conversation.ID, target.HistoryID, "run-fallback", "hello", target, ext); err != nil {
				t.Fatal(err)
			}
			if _, err := updateOwnedChatHistory(t.Context(), db, target.HistoryID, "run-fallback", map[string]any{"result": "before"}); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "manual-selection":
				if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Update("chat_model_version", conversation.ChatModelVersion+1).Error; err != nil {
					t.Fatal(err)
				}
			case "new-turn", "other-conversation":
				convID := conversation.ID
				if scenario == "other-conversation" {
					other, _, err := ensureConversation(t.Context(), db, "other-conversation", "Other", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: "fixed", ModelID: "model-a", Source: "own"})
					if err != nil {
						t.Fatal(err)
					}
					convID = other.ID
				}
				if err := registerForkableHistoryRun(t.Context(), db, convID, "new-history", "new-run", "new", chatPersistTarget{Seq: 2}, nil); err != nil {
					t.Fatal(err)
				}
			case "regenerated":
				if err := claimChatHistoryRun(t.Context(), db, target.HistoryID, "new-run"); err != nil {
					t.Fatal(err)
				}
			case "write-failure":
				if err := db.Callback().Update().Before("gorm:update").Register("selection-write-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "conversations" {
						tx.AddError(errors.New("controlled selection persistence failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer db.Callback().Update().Remove("selection-write-failure")
			}
			if _, err := acceptChatFallbackEvent(body, fallbackSwitchEvent(body, "run-fallback"), "run-fallback", &ext); err != nil {
				t.Fatal(err)
			}
			err := persistChatFallbackProgress(t.Context(), db, conversation.ID, target.HistoryID, "run-fallback", "hello", "", "after", 1, ext, nil, body)
			wantError := scenario == "regenerated" || scenario == "write-failure"
			if (err != nil) != wantError {
				t.Fatalf("persist error = %v", err)
			}
			var stored orm.Conversation
			if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			wantModel := "model-a"
			if scenario == "other-conversation" {
				wantModel = "model-b"
			}
			if stored.ChatModelID == nil || *stored.ChatModelID != wantModel {
				t.Fatalf("stored model = %v, want %s", stored.ChatModelID, wantModel)
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			if wantError && history.Result != "before" {
				t.Fatal("failed binding write partially committed history")
			}
		})
	}
}

func TestChatFallbackSelectionWriteFailureDoesNotPublishOrArchiveSwitch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, priorSwitch := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/prior-switch=%t", stream, priorSwitch), func(t *testing.T) {
				t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "false")
				db, conversation, body := fallbackTestBody(t, "fixed")
				seedAvailableChatModel(t, db, "user-1", "provider-c", "group-c", "model-c", "OpenAI", "C", "gpt-c", "llm", true, "fixture-c")
				if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
					t.Fatal(err)
				}
				fallbackState := body[chatModelFallbackBodyKey].(*chatFallbackState)
				wantModel, failedModel, wantSwitches := "model-a", "model-b", 0
				if priorSwitch {
					wantModel, failedModel, wantSwitches = "model-b", "model-c", 1
				}
				failedWrites, requests := 0, 0
				if err := db.Callback().Update().Before("gorm:update").Register("selection-consumer-failure", func(tx *gorm.DB) {
					values, _ := tx.Statement.Dest.(map[string]any)
					if tx.Statement.Table == "conversations" && values["chat_model_id"] == failedModel {
						failedWrites++
						tx.AddError(errors.New("controlled selection persistence failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer db.Callback().Update().Remove("selection-consumer-failure")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					var request LazyChatRequest
					_ = json.NewDecoder(r.Body).Decode(&request)
					encoder := json.NewEncoder(w)
					emit := func(data map[string]any) { _ = encoder.Encode(map[string]any{"code": 200, "data": data}) }
					model := func(index int, phase string, data map[string]any) {
						metadata := map[string]any{"run_id": request.Conversation.RunID, "call_index": index}
						if phase != "" {
							metadata["phase"] = phase
						}
						data["model_output"] = metadata
						emit(data)
					}
					model(1, "begin", map[string]any{})
					model(1, "", map[string]any{"think": "kept-think"})
					model(1, "", map[string]any{"text": "kept answer", "sources": []any{map[string]any{"id": "kept-reference"}}})
					commit := map[string]any{}
					if priorSwitch {
						commit["runtime_event"] = fallbackSwitchEvent(body, request.Conversation.RunID)
					}
					model(1, "commit", commit)
					emit(map[string]any{"text": "<tp>tool</tp><trp>kept tool result</trp>", "tool_call_turns": 1})
					if stream {
						emit(map[string]any{"artifact_created": map[string]any{"artifact_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "filename": "kept.txt", "content_type": "text", "value": map[string]any{"text": "kept artifact"}}})
					}
					model(2, "begin", map[string]any{})
					model(2, "", map[string]any{"think": "UNSAVED_THINK"})
					model(2, "", map[string]any{"text": "UNSAVED_TEXT", "sources": []any{map[string]any{"id": "UNSAVED_REFERENCE"}}, "export_snapshot": map[string]any{"content": "UNSAVED_EXPORT", "exports": []any{}}})
					event := fallbackWireEvent(request.Conversation.RunID, "unsaved-switch", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: wantSwitches + 1, From: fallbackState.identity("own:" + wantModel), To: fallbackState.identity("own:" + failedModel), ReasonCode: "not_found"})
					model(2, "commit", map[string]any{"runtime_event": event})
				}))
				defer server.Close()
				target := chatPersistTarget{HistoryID: "selection-failed-history", Seq: 1}
				recorder := httptest.NewRecorder()
				var stateStore state.Store
				if stream {
					stateStore = newRunDecisionTestStore(t)
					streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, stateStore, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
				} else {
					handleNonStreamChat(recorder, t.Context(), db, nil, server.URL, body, conversation.ID, "hello", target, mergeChatModelRouteIntoExt(nil, body))
				}
				if failedWrites != 1 || requests != 1 {
					t.Fatalf("write failures=%d upstream requests=%d, want one each", failedWrites, requests)
				}
				var history orm.ChatHistory
				if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
					t.Fatal(err)
				}
				var stored orm.Conversation
				if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
					t.Fatal(err)
				}
				if stored.ChatModelID == nil || *stored.ChatModelID != wantModel || history.RunStatus != "failed" || chatModelRouteFromHistoryExt(history.Ext).ModelID != wantModel || len(modelSwitchesFromExt(history.Ext)) != wantSwitches {
					t.Errorf("failed selection leaked: binding=%v status=%s ext=%s", stored.ChatModelID, history.RunStatus, history.Ext)
				}
				if strings.Contains(history.Result+string(history.Ext)+string(history.RetrievalResult), "UNSAVED") || !strings.Contains(history.Result, "kept answer") || strings.Count(history.Result, "kept tool result") != 1 || !strings.Contains(history.Result, "kept-think") || len(retrievalSources(history.RetrievalResult)) != 1 {
					t.Errorf("failed call changed committed projection: result=%q ext=%s sources=%s", history.Result, history.Ext, history.RetrievalResult)
				}
				if chatModelRouteFromBody(body).ModelID != wantModel || len(modelSwitchesFromBody(body)) != wantSwitches || llmConfigFromBody(body)["llm"].(map[string]any)["model"] != "gpt-"+strings.TrimPrefix(wantModel, "model-") {
					t.Error("failed transaction advanced the in-memory route")
				}
				if strings.Contains(recorder.Body.String(), "unsaved-switch") {
					t.Error("uncommitted switch was published")
				}
				cold := httptest.NewRecorder()
				resumeFromDBOnly(t.Context(), db, "user-1", conversation.ID, cold, cold)
				restored := decodeChatChunkSSE(t, cold.Body.String())[0]
				if restored.ModelRoute.ModelID != wantModel || len(restored.ModelSwitches) != wantSwitches || strings.Contains(restored.Delta+restored.ReasoningContent, "UNSAVED") || restored.ModelOutput.Phase != "rollback" {
					t.Errorf("cold resume differs from last durable output: %+v", restored)
				}
				if stream {
					chunks, _ := getChatChunks(t.Context(), stateStore, conversation.ID, target.HistoryID)
					merged := mergeChunksToFirstChunk(chunks)
					if strings.Contains(merged.Delta+merged.ReasoningContent, "UNSAVED") || merged.ModelRoute.ModelID != wantModel || len(merged.ModelSwitches) != wantSwitches || merged.ModelOutput.Phase != "rollback" {
						t.Errorf("cached stream did not retract failed projection: %+v", merged)
					}
					var artifact orm.ConversationArtifact
					if err := db.Where("id = ? AND conversation_id = ?", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", conversation.ID).Take(&artifact).Error; err != nil || !strings.Contains(string(artifact.Value), "kept artifact") {
						t.Errorf("completed artifact was lost: %v", err)
					}
				}
			})
		}
	}
}

func TestChatFallbackOldAutoTerminalCannotReplaceLatestRun(t *testing.T) {
	for _, regenerate := range []bool{false, true} {
		t.Run(fmt.Sprint(regenerate), func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, "auto")
			target := chatPersistTarget{HistoryID: "old-auto", Seq: 1}
			if err := registerForkableHistoryRun(t.Context(), db, conversation.ID, target.HistoryID, "run-fallback", "old", target, nil); err != nil {
				t.Fatal(err)
			}
			if regenerate {
				if err := claimChatHistoryRun(t.Context(), db, target.HistoryID, "new-run"); err != nil {
					t.Fatal(err)
				}
			} else if err := registerForkableHistoryRun(t.Context(), db, conversation.ID, "new-auto", "new-run", "new", chatPersistTarget{Seq: 2}, nil); err != nil {
				t.Fatal(err)
			}
			var ext json.RawMessage
			if _, err := acceptChatFallbackEvent(body, fallbackSwitchEvent(body, "run-fallback"), "run-fallback", &ext); err != nil {
				t.Fatal(err)
			}
			persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "run-fallback", body, &RunTerminal{Status: "completed", Reason: "normal"})
			var stored orm.Conversation
			if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if len(stored.ChatModelSnapshot) != 0 {
				t.Fatal("old Auto terminal replaced the latest run's selection")
			}
		})
	}
}
func TestChatFallbackPreparationWaitsForHostSuccess(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-a").Update("deleted_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
		t.Fatal(err)
	}
	state := body[chatModelFallbackBodyKey].(*chatFallbackState)
	request := buildLazyChatRequest(body)
	if len(state.records) != 0 || request.Runtime.ModelFallbackInitialSwitch == nil || state.initialSwitch.From.ModelID != "model-a" || request.Runtime.ModelFallbackCurrentID != "own:model-b" {
		t.Fatal("bootstrap must remain private until a real Host success")
	}
	if chatModelRouteFromBody(body).ModelID != "model-a" {
		t.Fatal("preparation changed effective route")
	}
	var ext json.RawMessage
	event := fallbackWireEvent("run-fallback", "bootstrap-success", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: 1, From: state.initialSwitch.From, To: state.identity("own:model-b"), ReasonCode: "model_config_unavailable"})
	if _, err := acceptChatFallbackEvent(body, event, "run-fallback", &ext); err != nil {
		t.Fatal(err)
	}
	if len(state.records) != 1 || chatModelRouteFromBody(body).ModelID != "model-b" {
		t.Fatal("Host success did not commit the bootstrap replacement")
	}
	var stored orm.Conversation
	_ = db.Where("id = ?", conversation.ID).Take(&stored).Error
	if string(stored.ChatModelSnapshot) != string(conversation.ChatModelSnapshot) {
		t.Fatal("preparation changed fixed snapshot")
	}
}

func TestChatFallbackAutoSuccessCannotOverwriteNewSelectionVersion(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "auto")
	var ext json.RawMessage
	_, _ = acceptChatFallbackEvent(body, fallbackSwitchEvent(body, "run-fallback"), "run-fallback", &ext)
	_, err := acceptChatFallbackEvent(body, fallbackFinishedEvent("run-fallback", "call-b", "stop"), "run-fallback", &ext)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Update("chat_model_version", conversation.ChatModelVersion+1).Error; err != nil {
		t.Fatal(err)
	}
	invoked := false
	persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "run-fallback", body, &RunTerminal{Status: "completed", Reason: "normal", ModelInvoked: &invoked})
	var stored orm.Conversation
	_ = db.Where("id = ?", conversation.ID).Take(&stored).Error
	if len(stored.ChatModelSnapshot) != 0 {
		t.Fatal("stale success overwrote newer model selection")
	}
}
func TestChatFallbackWireIgnoresForgedClientCandidates(t *testing.T) {
	db, conversation, _ := fallbackTestBody(t, "fixed")
	body := map[string]any{"conversation_id": conversation.ID, "model_fallback_current_id": "own:forged", "model_fallback_candidates": []any{map[string]any{"api_key": "forged-secret"}}, chatModelFallbackBodyKey: map[string]any{"candidates": "forged"}, chatModelPreparedBodyKey: map[string]any{"forged": true}}
	if err := applyChatRuntimeConfigs(t.Context(), db, "user-1", body); err != nil {
		t.Fatal(err)
	}
	request := buildLazyChatRequest(body)
	raw, _ := json.Marshal(request)
	if strings.Contains(string(raw), "forged") || request.Runtime.ModelFallbackCurrentID != "own:model-a" {
		t.Fatal("untrusted candidate input was forwarded")
	}
}

func TestChatFallbackUnknownInitialBindingStillRejectsExecution(t *testing.T) {
	db := newChatExecutionTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	seedAvailableChatModel(t, db, "user-1", "provider-b", "group-b", "model-b", "OpenAI", "B", "gpt-b", "llm", true, "fixture-b")
	request := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", strings.NewReader(`{"conversation_id":"missing-initial-binding","stream":true,"input":[{"input_type":"text","text":"hello"}],"initial_model_selection":{"mode":"fixed","model_id":"missing-model"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()
	ChatConversations(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("untrusted initial ID was recovered: status=%d response=%s", recorder.Code, recorder.Body.String())
	}
	var conversation orm.Conversation
	if err := db.Where("id = ?", "missing-initial-binding").Take(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.ChatModelID == nil || *conversation.ChatModelID != "missing-model" || len(conversation.ChatModelSnapshot) != 0 {
		t.Fatal("execution refusal changed the existing binding behavior")
	}
}

func TestChatFallbackResumeRestoresRecordsWithoutReplayingAttemptEvents(t *testing.T) {
	_, _, body := fallbackTestBody(t, "fixed")
	var ext json.RawMessage
	switched := fallbackSwitchEvent(body, "run-fallback")
	_, _ = acceptChatFallbackEvent(body, switched, "run-fallback", &ext)
	_, _ = acceptChatFallbackEvent(body, fallbackFinishedEvent("run-fallback", "call-b", "stop"), "run-fallback", &ext)
	terminal := completedRunEvent("run-fallback", true)
	outcome, _ := terminal.Terminal()
	stateStore := newRunDecisionTestStore(t)
	for _, chunk := range []*ChatChunkResponse{{ConversationID: "conv", HistoryID: "history", RuntimeEvent: switched, ModelSwitches: modelSwitchesFromBody(body)}, {ConversationID: "conv", HistoryID: "history", Delta: "answer", ModelSwitches: modelSwitchesFromBody(body), ModelRoute: chatModelRouteFromBody(body)}, {ConversationID: "conv", HistoryID: "history", RuntimeEvent: terminal}} {
		if err := appendChatChunk(t.Context(), stateStore, "conv", "history", chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := setChatRuntimeStatus(t.Context(), stateStore, "conv", "history", "completed", "answer", "run-fallback", outcome); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	resumeSingleAnswerChat(t.Context(), stateStore, "conv", "history", recorder, recorder)
	found := false
	for _, chunk := range decodeChatChunkSSE(t, recorder.Body.String()) {
		if chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventModelSwitched {
			t.Fatal("resume replayed a realtime attempt")
		}
		if len(chunk.ModelSwitches) > 0 {
			found = true
			if chunk.ModelRoute.ModelID != "model-b" {
				t.Fatal("resume lost confirmed model facts")
			}
		}
	}
	if !found {
		t.Fatal("resume omitted replacement records")
	}
}

func TestChatFallbackRejectsOutOfOrderSuccessfulSwitch(t *testing.T) {
	_, _, body := fallbackTestBody(t, "auto")
	var ext json.RawMessage
	if _, err := acceptChatFallbackEvent(body, fallbackSwitchEvent(body, "run-fallback"), "run-fallback", &ext); err != nil {
		t.Fatal(err)
	}
	state := body[chatModelFallbackBodyKey].(*chatFallbackState)
	event := fallbackWireEvent("run-fallback", "stale-from", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: 2, From: state.identity("own:model-a"), To: state.identity("own:model-b"), ReasonCode: "not_found"})
	if _, err := acceptChatFallbackEvent(body, event, "run-fallback", &ext); err == nil {
		t.Fatal("out-of-order original identity was accepted")
	}
}

func TestChatFallbackFailedCallCreatesNoSuccessfulSwitch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, "fixed")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request LazyChatRequest
				_ = json.NewDecoder(r.Body).Decode(&request)
				encoder := json.NewEncoder(w)
				for _, data := range []map[string]any{{"text": "earlier completed output"}, {"runtime_event": failedRunEvent(request.Conversation.RunID, "formatter_failed", true)}} {
					_ = encoder.Encode(map[string]any{"code": 200, "data": data})
				}
			}))
			defer server.Close()
			target := chatPersistTarget{HistoryID: "unconfirmed-history", Seq: 1}
			ext := mergeChatModelRouteIntoExt(nil, body)
			recorder := httptest.NewRecorder()
			if stream {
				streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, ext)
			} else {
				handleNonStreamChat(recorder, t.Context(), db, nil, server.URL, body, conversation.ID, "hello", target, ext)
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			records := modelSwitchesFromExt(history.Ext)
			if history.RunStatus != "failed" || len(records) != 0 || chatModelRouteFromHistoryExt(history.Ext).ModelID != "model-a" {
				t.Fatalf("unconfirmed attempt survived terminal: status=%s ext=%s", history.RunStatus, history.Ext)
			}
		})
	}
}
func TestChatFallbackAutoDeletedStickyKeepsPrivateBootstrap(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, "auto")
			persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "earlier-success", body, &RunTerminal{Status: "completed", Reason: "normal"})
			var before orm.Conversation
			_ = db.Where("id = ?", conversation.ID).Take(&before).Error
			if deleted {
				if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-a").Update("deleted_at", time.Now()).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "user-1").Update("user_model_provider_group_model_id", "model-b").Error; err != nil {
				t.Fatal(err)
			}
			next := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
			if err := applyConversationChatModelConfig(t.Context(), db, "user-1", next); err != nil {
				t.Fatal(err)
			}
			records := modelSwitchesFromBody(next)
			if deleted {
				initial := next[chatModelFallbackBodyKey].(*chatFallbackState).initialSwitch
				if len(records) != 0 || initial == nil || initial.From.ModelID != "model-a" || chatModelRouteFromBody(next).ModelID != "model-a" {
					t.Fatalf("missing sticky failure preparation: %#v", records)
				}
			} else {
				if len(records) != 0 || next["llm_config"].(map[string]any)["llm"].(map[string]any)["model"] != "gpt-a" {
					t.Fatal("healthy sticky model was announced as unavailable")
				}
			}
			var after orm.Conversation
			_ = db.Where("id = ?", conversation.ID).Take(&after).Error
			if string(after.ChatModelSnapshot) != string(before.ChatModelSnapshot) {
				t.Fatal("preparation changed successful Auto snapshot")
			}
		})
	}
}
func TestChatFallbackBrokenCredentialIsSkippedBeforeClientConstruction(t *testing.T) {
	db, conversation, _ := fallbackTestBody(t, "fixed")
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Update("api_key_ciphertext", "fixture-invalid-envelope").Error; err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
	if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
		t.Fatal(err)
	}
	request := buildLazyChatRequest(body)
	if request.Runtime.ModelFallbackCurrentID != "own:model-b" || len(request.Runtime.ModelFallbackCandidates) != 1 || request.Runtime.LLMConfig["llm"].(map[string]any)["model"] != "gpt-b" {
		t.Fatal("broken credential remained the client constructed before fallback")
	}
	if state := body[chatModelFallbackBodyKey].(*chatFallbackState); len(state.records) != 0 || state.initialSwitch == nil {
		t.Fatal("missing safe preparation replacement")
	}
}

func TestChatFallbackCandidateCredentialHeaderSafety(t *testing.T) {
	for _, test := range []struct {
		name, key, current, want string
		replacement              bool
	}{
		{name: "keyless endpoint", key: " \n ", current: "own:model-a", want: ""},
		{name: "encrypted outer whitespace", key: " \r\nfixture-single\t ", current: "own:model-a", want: "fixture-single"},
		{name: "access secret pair", key: "fixture-access:fixture-secret", current: "own:model-a", want: "fixture-access:fixture-secret"},
		{name: "embedded LF", key: "fixture-first\nfixture-second", current: "own:model-b", want: "fixture-b", replacement: true},
		{name: "embedded CR", key: "fixture-first\rfixture-second", current: "own:model-b", want: "fixture-b", replacement: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, conversation, _ := fallbackTestBody(t, "fixed")
			before := string(conversation.ChatModelSnapshot)
			const fixtureEncryptionKey = "fixture-local-credential-encryption-key"
			t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", fixtureEncryptionKey)
			ciphertext, err := secretcrypto.EncodeAESGCM([]byte(test.key), fixtureEncryptionKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Updates(map[string]any{"api_key": "", "api_key_ciphertext": string(ciphertext)}).Error; err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
			if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
				t.Fatal(err)
			}
			request := buildLazyChatRequest(body)
			if request.Runtime.ModelFallbackCurrentID != test.current || request.Runtime.LLMConfig["llm"].(map[string]any)["api_key"] != test.want {
				t.Fatal("dynamic current model retained an unusable credential")
			}
			if test.want == "" && request.Runtime.LLMConfig["llm"].(map[string]any)["skip_auth"] != true {
				t.Fatal("keyless current model lost skip_auth")
			}
			found := false
			for _, candidate := range request.Runtime.ModelFallbackCandidates {
				if candidate.CandidateID == test.current {
					found = true
					if candidate.LLMConfig["llm"].(map[string]any)["api_key"] != test.want {
						t.Fatal("static candidate and dynamic current credentials differ")
					}
					if test.want == "" && candidate.LLMConfig["llm"].(map[string]any)["skip_auth"] != true {
						t.Fatal("keyless fallback candidate lost skip_auth")
					}
				}
				if test.replacement && candidate.ProviderGroupID == "group-a" {
					t.Fatal("newline credential reached a private candidate")
				}
			}
			if !found {
				t.Fatal("current model was not present in its authorized candidate pool")
			}
			if test.replacement {
				state := body[chatModelFallbackBodyKey].(*chatFallbackState)
				if len(state.records) != 0 || state.initialSwitch == nil || state.initialSwitch.ReasonCode != "model_config_unavailable" {
					t.Fatal("unsupported credential format was reported as provider key invalidity")
				}
			}
			persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "fixture-run", body, &RunTerminal{Status: "completed", Reason: "normal"})
			var stored orm.Conversation
			if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if stored.ChatModelID == nil || *stored.ChatModelID != "model-a" || string(stored.ChatModelSnapshot) != before {
				t.Fatal("credential preparation changed fixed binding or snapshot")
			}
		})
	}
}
