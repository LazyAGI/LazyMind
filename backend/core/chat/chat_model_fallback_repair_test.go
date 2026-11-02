package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func hostFallbackSwitch(body map[string]any, runID string) *ChatRuntimeEvent {
	state := body[chatModelFallbackBodyKey].(*chatFallbackState)
	return fallbackWireEvent(runID, "host-switch-b", RuntimeEventModelSwitched, map[string]any{
		"switch_index": 1, "from": state.identity("own:model-a"), "to": state.identity("own:model-b"),
		"reason_code": "not_found", "from_max_input_tokens": nil, "to_max_input_tokens": nil,
		"context_compacted": false,
	})
}

func TestChatFallbackHostSuccessNeedsNoSDKLifecycle(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "auto")
	var ext json.RawMessage
	event := hostFallbackSwitch(body, "run-fallback")
	if accepted, err := acceptChatFallbackEvent(body, event, "run-fallback", &ext); err != nil || !accepted {
		t.Fatalf("host success rejected: accepted=%t err=%v", accepted, err)
	}
	if chatModelRouteFromBody(body).ModelID != "model-b" || llmConfigFromBody(body)["llm"].(map[string]any)["model"] != "gpt-b" {
		t.Fatal("host success did not promote the effective route and configuration")
	}
	invoked := false // Native SDK paths have no lifecycle event to set this flag.
	terminal := &RunTerminal{Status: "completed", Reason: "normal", ModelInvoked: &invoked}
	persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "run-fallback", body, terminal)
	var stored orm.Conversation
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot chatModelSnapshot
	_ = json.Unmarshal(stored.ChatModelSnapshot, &snapshot)
	if snapshot.ModelID != "model-b" || snapshot.SuccessfulRunID != "run-fallback" {
		t.Fatal("confirmed native fallback did not save Auto's successful model")
	}
	if got := successfulChatModelSnapshot(conversation, []chatModelHistory{{RunID: "run-fallback", RunTerminal: terminalJSON(terminal), Ext: ext}}); got == nil || got.ModelID != "model-b" {
		t.Fatal("history recovery ignored a confirmed native fallback")
	}
}

func TestChatFallbackArchivePreservesPreviousSuccessfulSwitches(t *testing.T) {
	oldExt := json.RawMessage(`{"model_switches":[{"event_id":"old-switch","switch_index":1,"from":{"candidate_id":"own:a","source":"own","model_id":"a","provider_group_id":"ga"},"to":{"candidate_id":"own:b","source":"own","model_id":"b","provider_group_id":"gb"},"reason_code":"not_found","from_max_input_tokens":null,"to_max_input_tokens":null,"context_compacted":false}]}`)
	terminal := &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "formatter_failed", PartialOutput: true}
	target := chatPersistTarget{IsRegeneration: true, Existing: &orm.ChatHistory{
		RunID: "old-run", RunStatus: "failed", Result: "earlier committed output", RunTerminal: terminalJSON(terminal), Ext: oldExt,
		TimeMixin: orm.TimeMixin{CreateTime: time.Now()},
	}}
	archived := archiveRegeneratedFailedRunAttempt(nil, target)
	archived = mergeModelSwitchesIntoExt(archived, nil)
	var ext struct {
		Attempts []struct {
			RunID    string              `json:"run_id"`
			Switches []ModelSwitchRecord `json:"model_switches"`
		} `json:"failed_run_attempts"`
	}
	if err := json.Unmarshal(archived, &ext); err != nil {
		t.Fatal(err)
	}
	if len(ext.Attempts) != 1 || ext.Attempts[0].RunID != "old-run" || len(ext.Attempts[0].Switches) != 1 || ext.Attempts[0].Switches[0].EventID != "old-switch" {
		t.Fatalf("new run discarded previous switch facts: %s", archived)
	}
}

func TestChatFallbackEmptyNonstreamKeepsAuthoritativeFailure(t *testing.T) {
	for _, code := range []string{"model_fallback_exhausted", "model_context_not_fit"} {
		t.Run(code, func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, "fixed")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request LazyChatRequest
				_ = json.NewDecoder(r.Body).Decode(&request)
				event := runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "failed", Reason: "model_failure", Code: code})
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": event}})
			}))
			defer server.Close()
			target := chatPersistTarget{HistoryID: "empty-fallback-history", Seq: 1}
			recorder := httptest.NewRecorder()
			handleNonStreamChat(recorder, t.Context(), db, nil, server.URL, body, conversation.ID, "hello", target, mergeChatModelRouteIntoExt(nil, body))
			if recorder.Code != http.StatusOK {
				t.Errorf("authoritative failure response=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			terminal, err := parseRunTerminal(history.RunTerminal)
			if err != nil || terminal.Reason != "model_failure" || terminal.Code != code || history.RunStatus != "failed" {
				t.Fatalf("authoritative terminal was overwritten: terminal=%s err=%v", history.RunTerminal, err)
			}
		})
	}
}

func TestChatFallbackBootstrapCanCommitLaterAuthorizedCandidate(t *testing.T) {
	for _, mode := range []string{"fixed", "auto"} {
		t.Run(mode, func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, mode)
			if mode == "auto" {
				persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "earlier-success", body, &RunTerminal{Status: "completed", Reason: "normal"})
			}
			seedAvailableChatModel(t, db, "user-1", "provider-c", "group-c", "model-c", "OpenAI", "C", "gpt-c", "llm", true, "fixture-c")
			for id, window := range map[string]string{"model-b": "8192", "model-c": "4096"} {
				if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", id).Update("max_input_tokens", window).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-a").Update("deleted_at", time.Now()).Error; err != nil {
				t.Fatal(err)
			}
			next := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1", "run_id": "run-bootstrap"}
			if err := applyConversationChatModelConfig(t.Context(), db, "user-1", next); err != nil {
				t.Fatal(err)
			}
			state := next[chatModelFallbackBodyKey].(*chatFallbackState)
			if state.currentID != "own:model-b" || state.candidate("own:model-a") != nil || state.initialSwitch == nil || state.initialSwitch.From.ModelID != "model-a" || chatModelRouteFromBody(next).ModelID != "model-a" || len(state.records) != 0 {
				t.Fatal("bootstrap did not separate private starting model from trusted original route")
			}
			var ext json.RawMessage
			event := fallbackWireEvent("run-bootstrap", "success-c", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: 1, From: state.initialSwitch.From, To: state.identity("own:model-c"), ReasonCode: "model_config_unavailable"})
			if accepted, err := acceptChatFallbackEvent(next, event, "run-bootstrap", &ext); err != nil || !accepted {
				t.Fatalf("A to C host success rejected: %t %v", accepted, err)
			}
			if chatModelRouteFromBody(next).ModelID != "model-c" || llmConfigFromBody(next)["llm"].(map[string]any)["model"] != "gpt-c" || len(modelSwitchesFromExt(ext)) != 1 || modelSwitchesFromExt(ext)[0].ToMaxInputTokens == nil || *modelSwitchesFromExt(ext)[0].ToMaxInputTokens != 4096 {
				t.Fatal("bootstrap success did not publish the actual successful candidate")
			}
			invoked := false
			if err := persistChatFallbackProgress(t.Context(), db, conversation.ID, "bootstrap-selection", "run-bootstrap", "hello", "", "", 1, ext, nil, next); err != nil {
				t.Fatal(err)
			}
			persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "run-bootstrap", next, &RunTerminal{Status: "completed", Reason: "normal", ModelInvoked: &invoked})
			var stored orm.Conversation
			_ = db.Where("id = ?", conversation.ID).Take(&stored).Error
			var snapshot chatModelSnapshot
			_ = json.Unmarshal(stored.ChatModelSnapshot, &snapshot)
			if mode == "fixed" && (snapshot.ModelID != "model-c" || *stored.ChatModelID != "model-c") || mode == "auto" && (snapshot.ModelID != "model-c" || snapshot.SuccessfulRunID != "run-bootstrap") {
				t.Fatalf("wrong binding after bootstrap success: mode=%s snapshot=%s", mode, stored.ChatModelSnapshot)
			}
		})
	}
}

func TestChatFallbackNativeHistoryEvidenceRequiresMatchingRouteAndSelection(t *testing.T) {
	_, conversation, body := fallbackTestBody(t, "auto")
	var ext json.RawMessage
	if _, err := acceptChatFallbackEvent(body, hostFallbackSwitch(body, "run-fallback"), "run-fallback", &ext); err != nil {
		t.Fatal(err)
	}
	invoked := false
	history := chatModelHistory{RunID: "run-fallback", RunTerminal: terminalJSON(&RunTerminal{Status: "completed", Reason: "normal", ModelInvoked: &invoked}), Ext: ext}
	conversation.ChatModelVersion++
	if successfulChatModelSnapshot(conversation, []chatModelHistory{history}) != nil {
		t.Fatal("old selection's native success crossed the selection version")
	}
	conversation.ChatModelVersion--
	body[chatModelRouteBodyKey].(*chatModelRoute).ModelID = "model-a"
	history.Ext = mergeChatModelRouteIntoExt(ext, body)
	if successfulChatModelSnapshot(conversation, []chatModelHistory{history}) != nil {
		t.Fatal("unrelated successful switch confirmed a different effective route")
	}
}

func TestChatFallbackDeletedAutoRetryUsesTrustedHistoryRoute(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "auto")
	prior := chatModelRouteFromBody(body)
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-a").Update("deleted_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	next := map[string]any{"conversation_id": conversation.ID, chatModelRetryRouteBodyKey: prior}
	if err := applyConversationChatModelConfig(t.Context(), db, "user-1", next); err != nil {
		t.Fatal(err)
	}
	state := next[chatModelFallbackBodyKey].(*chatFallbackState)
	if state.initialSwitch == nil || state.initialSwitch.From.ModelID != "model-a" || state.currentID != "own:model-b" || chatModelRouteFromBody(next).ModelID != "model-a" {
		t.Fatal("validated retry history route was lost during preparation")
	}
}

func TestChatFallbackConfiguredBootstrapOriginalPersistsHostOutcome(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		name := "exhausted"
		if succeeds {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			db, conversation, priorBody := fallbackTestBody(t, "auto")
			invoked := true
			priorExt := mergeChatModelRouteIntoExt(nil, priorBody)
			success := &RunTerminal{Status: "completed", Reason: "normal", ModelInvoked: &invoked}
			if !persistImmediateRunTerminal(t.Context(), db, conversation.ID, "prior-success-a", "earlier turn", "run-prior-success-a", chatPersistTarget{Seq: 1}, priorExt, success) {
				t.Fatal("could not persist A's successful history")
			}
			persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "run-prior-success-a", priorBody, success)
			failure := &RunTerminal{Status: "failed", Reason: "model_failure", Code: "service_unavailable", ModelInvoked: &invoked}
			if !persistImmediateRunTerminal(t.Context(), db, conversation.ID, "prior-failed-a", "later turn", "run-prior-failed-a", chatPersistTarget{Seq: 2}, priorExt, failure) {
				t.Fatal("could not persist A's later failure")
			}
			var before orm.Conversation
			if err := db.Where("id = ?", conversation.ID).Take(&before).Error; err != nil {
				t.Fatal(err)
			}
			seedAvailableChatModel(t, db, "user-1", "provider-c", "group-c", "model-c", "OpenAI", "C", "gpt-c", "llm", true, "fixture-c")
			body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1", "run_id": "run-bootstrap-current"}
			if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
				t.Fatal(err)
			}
			state := body[chatModelFallbackBodyKey].(*chatFallbackState)
			if state.currentID != "own:model-b" || state.initialSwitch == nil || state.initialSwitch.From.CandidateID != "own:model-a" || state.candidate("own:model-a") == nil || chatModelRouteFromBody(body).ModelID != "model-a" {
				t.Fatal("configured A and its trusted failure did not produce the expected private bootstrap")
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				originalPresent := false
				var target ModelSwitchIdentity
				for _, candidate := range request.Runtime.ModelFallbackCandidates {
					originalPresent = originalPresent || candidate.CandidateID == "own:model-a"
					if candidate.CandidateID == "own:model-c" {
						target = ModelSwitchIdentity{CandidateID: candidate.CandidateID, Source: candidate.Source, ModelID: candidate.ModelID, ProviderGroupID: candidate.ProviderGroupID}
					}
				}
				initial := request.Runtime.ModelFallbackInitialSwitch
				if !originalPresent || initial == nil || initial.From.ModelID != "model-a" || initial.ReasonCode != "model_config_unavailable" || request.Runtime.ModelFallbackCurrentID != "own:model-b" || request.Runtime.LLMConfig["llm"].(map[string]any)["model"] != "gpt-b" {
					t.Error("formal request lost the configured original or actual bootstrap model")
					return
				}
				encoder := json.NewEncoder(w)
				emit := func(data map[string]any) { _ = encoder.Encode(map[string]any{"code": 200, "data": data}) }
				emit(map[string]any{"runtime_event": fallbackWireEvent(request.Conversation.RunID, "b-failed", RuntimeEventModelCallFinished, map[string]any{"model_call_id": "call-b", "attempt_count": 1, "kind": "failure", "has_semantic_output": false, "failure": map[string]any{"origin": "http", "code": "service_unavailable"}})})
				if succeeds {
					emit(map[string]any{"runtime_event": fallbackWireEvent(request.Conversation.RunID, "host-success-c", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: 1, From: initial.From, To: target, ReasonCode: "model_config_unavailable", FromMaxInputTokens: initial.FromMaxInputTokens})})
					emit(map[string]any{"text": "answer-c"})
					emit(map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true, ModelInvoked: &invoked})})
				} else {
					emit(map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "failed", Reason: "model_failure", Code: "model_fallback_exhausted", ModelInvoked: &invoked})})
				}
			}))
			defer server.Close()
			recorder := httptest.NewRecorder()
			target := chatPersistTarget{HistoryID: "bootstrap-current", Seq: 3}
			streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "current turn", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
			var history orm.ChatHistory
			if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			terminal, err := parseRunTerminal(history.RunTerminal)
			if err != nil || calls != 1 || history.RunID != "run-bootstrap-current" {
				t.Fatalf("invalid persisted execution: calls=%d terminal=%s err=%v", calls, history.RunTerminal, err)
			}
			var after orm.Conversation
			if err := db.Where("id = ?", conversation.ID).Take(&after).Error; err != nil {
				t.Fatal(err)
			}
			var snapshot chatModelSnapshot
			_ = json.Unmarshal(after.ChatModelSnapshot, &snapshot)
			records := modelSwitchesFromExt(history.Ext)
			if succeeds {
				if terminal.Status != "completed" || history.RunStatus != "completed" || history.Result != "answer-c" || chatModelRouteFromHistoryExt(history.Ext).ModelID != "model-c" || chatModelRouteFromBody(body).ModelID != "model-c" || len(records) != 1 || records[0].From.ModelID != "model-a" || records[0].To.ModelID != "model-c" || records[0].SwitchIndex != 1 || snapshot.ModelID != "model-c" || snapshot.SuccessfulRunID != history.RunID {
					t.Fatalf("configured-original success was not persisted: terminal=%s ext=%s snapshot=%s", history.RunTerminal, history.Ext, after.ChatModelSnapshot)
				}
			} else if terminal.Status != "failed" || history.RunStatus != "failed" || terminal.Reason != "model_failure" || terminal.Code != "model_fallback_exhausted" || history.Result != "" || len(records) != 0 || chatModelRouteFromHistoryExt(history.Ext).ModelID != "model-a" || string(after.ChatModelSnapshot) != string(before.ChatModelSnapshot) {
				t.Fatalf("exhaustion became a protocol failure or false success: terminal=%s ext=%s snapshot=%s", history.RunTerminal, history.Ext, after.ChatModelSnapshot)
			}
			terminals := 0
			for _, chunk := range decodeChatChunkSSE(t, recorder.Body.String()) {
				if chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventRunFinished {
					terminals++
					visible, err := chunk.RuntimeEvent.Terminal()
					if err != nil || visible.Status != terminal.Status || visible.Code != terminal.Code {
						t.Fatal("visible terminal differs from durable terminal")
					}
				}
			}
			if terminals != 1 {
				t.Fatalf("visible terminal count=%d, want 1", terminals)
			}
		})
	}
}

func TestChatFallbackCompactedSwitchMetadataSurvivesHistoryAndResume(t *testing.T) {
	for _, mode := range []string{"fixed", "auto"} {
		t.Run(mode, func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, mode)
			for id, window := range map[string]string{"model-a": "16384", "model-b": "4096"} {
				if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", id).Update("max_input_tokens", window).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				state := body[chatModelFallbackBodyKey].(*chatFallbackState)
				// Core must use its authorized declarations, rather than these upstream values.
				fromWindow, toWindow := int64(32768), int64(8192)
				switched := fallbackWireEvent(request.Conversation.RunID, "compacted-b", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: 1, From: state.identity("own:model-a"), To: state.identity("own:model-b"), ReasonCode: "token_limit", FromMaxInputTokens: &fromWindow, ToMaxInputTokens: &toWindow, ContextCompacted: true})
				invoked := true
				encoder := json.NewEncoder(w)
				for _, data := range []map[string]any{{"runtime_event": switched}, {"text": "short-window answer"}, {"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true, ModelInvoked: &invoked})}} {
					_ = encoder.Encode(map[string]any{"code": 200, "data": data})
				}
			}))
			defer server.Close()
			target := chatPersistTarget{HistoryID: "compacted-history", Seq: 1}
			recorder := httptest.NewRecorder()
			streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
			checkRecords := func(records []ModelSwitchRecord) {
				t.Helper()
				if len(records) != 1 || records[0].EventID != "compacted-b" || records[0].SwitchIndex != 1 || records[0].From.ModelID != "model-a" || records[0].To.ModelID != "model-b" || !records[0].ContextCompacted || records[0].FromMaxInputTokens == nil || *records[0].FromMaxInputTokens != 16384 || records[0].ToMaxInputTokens == nil || *records[0].ToMaxInputTokens != 4096 {
					t.Fatalf("compacted switch metadata changed: %#v", records)
				}
			}
			foundEvent := false
			for _, chunk := range decodeChatChunkSSE(t, recorder.Body.String()) {
				if chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventModelSwitched {
					foundEvent = true
					var data ModelSwitchedData
					if err := json.Unmarshal(chunk.RuntimeEvent.Data, &data); err != nil {
						t.Fatal(err)
					}
					checkRecords([]ModelSwitchRecord{{ModelSwitchedData: data, EventID: chunk.RuntimeEvent.EventID}})
					checkRecords(chunk.ModelSwitches)
				}
			}
			if !foundEvent {
				t.Fatal("stream omitted the committed compacted switch")
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			if history.RunStatus != "completed" || history.Result != "short-window answer" || chatModelRouteFromHistoryExt(history.Ext).ModelID != "model-b" {
				t.Fatalf("wrong compacted result: status=%s ext=%s", history.RunStatus, history.Ext)
			}
			checkRecords(modelSwitchesFromExt(history.Ext))
			checkRecords(chatHistoryToResponseItem(history)["model_switches"].([]ModelSwitchRecord))
			resume := httptest.NewRecorder()
			resumeFromDBOnly(t.Context(), db, "user-1", conversation.ID, resume, resume)
			chunks := decodeChatChunkSSE(t, resume.Body.String())
			if len(chunks) < 1 || chunks[0].ModelRoute == nil || chunks[0].ModelRoute.ModelID != "model-b" || calls != 1 {
				t.Fatal("database resume lost the effective model or restarted generation")
			}
			checkRecords(chunks[0].ModelSwitches)
			var stored orm.Conversation
			if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if mode == "fixed" {
				if stored.ChatModelID == nil || *stored.ChatModelID != "model-b" {
					t.Fatal("compacted replacement did not update the fixed selection")
				}
			} else {
				var snapshot chatModelSnapshot
				_ = json.Unmarshal(stored.ChatModelSnapshot, &snapshot)
				if snapshot.ModelID != "model-b" || snapshot.SuccessfulRunID != history.RunID {
					t.Fatal("Auto did not retain the actual compacted model")
				}
			}
		})
	}
}
