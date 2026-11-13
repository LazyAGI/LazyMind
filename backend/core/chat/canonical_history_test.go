package chat

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCanonicalHistoryThroughUpstreamStreamAndDatabase(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		emit := func(data map[string]any) { _ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": data}) }
		identity := func(index int, phase string) map[string]any {
			return map[string]any{"run_id": request.Conversation.RunID, "call_index": index, "phase": phase}
		}
		emit(map[string]any{"model_output": identity(1, "begin")})
		emit(map[string]any{"text": "saved"})
		emit(map[string]any{"canonical_messages": []map[string]any{{"role": "assistant", "content": "saved", "_lazyllm_provider_items": map[string]any{"protocol": "responses", "items": []any{map[string]any{"type": "reasoning", "encrypted_content": "SECRET"}}}}}})
		emit(map[string]any{"model_output": identity(1, "commit")})
		emit(map[string]any{"model_output": identity(2, "begin")})
		emit(map[string]any{"canonical_messages": []map[string]any{{"role": "assistant", "content": "FAILED"}}})
		emit(map[string]any{"model_output": identity(2, "rollback")})
		emit(map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "failed", Reason: "model_failure", Code: "model_fallback_exhausted", PartialOutput: true})})
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "question", "canonical-history", chatPersistTarget{HistoryID: "canonical-history", Seq: 1}, mergeChatModelRouteIntoExt(nil, body))
	var history orm.ChatHistory
	if err := db.Where("id = ?", "canonical-history").Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	restored, _ := json.Marshal(buildHistoryMessages([]orm.ChatHistory{history}, nil))
	if !strings.Contains(string(restored), "SECRET") || strings.Contains(string(restored), "FAILED") {
		t.Fatalf("bad durable replay: %s", restored)
	}
	if strings.Contains(recorder.Body.String(), "SECRET") || strings.Contains(recorder.Body.String(), "canonical_messages") {
		t.Fatal("private continuation leaked to SSE")
	}
}

func TestCanonicalHistoryRollbackAndPrivateProjection(t *testing.T) {
	owner := newChatModelOutput("run")
	output := chatOutputProjection{}
	kept := map[string]any{"role": "assistant", "content": "kept", "_lazyllm_provider_items": map[string]any{"protocol": "responses", "items": []any{map[string]any{"type": "reasoning", "encrypted_content": "SECRET"}}}}
	output.appendCanonical([]map[string]any{kept})
	owner.apply(&ChatModelOutput{RunID: "run", CallIndex: 1, Phase: "begin"}, &output)
	output.appendCanonical([]map[string]any{{"role": "assistant", "content": "FAILED"}})
	open := owner.ext(nil, output, "c", "h")
	if strings.Contains(string(open), "FAILED") {
		t.Fatal("uncommitted message persisted")
	}
	owner.apply(&ChatModelOutput{RunID: "run", CallIndex: 1, Phase: "rollback"}, &output)
	history := orm.ChatHistory{ID: "h", RunID: "run", RawContent: "question", Result: "rendered", Ext: owner.ext(nil, output, "c", "h")}
	restored := buildHistoryMessages([]orm.ChatHistory{history}, nil)
	raw, _ := json.Marshal(restored)
	if !strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "FAILED") || strings.Contains(string(raw), "rendered") {
		t.Fatalf("bad replay: %s", raw)
	}
	public, _ := json.Marshal(chatHistoryToResponseItem(history))
	if strings.Contains(string(public), "SECRET") || strings.Contains(string(public), "canonical_messages") {
		t.Fatalf("private state leaked: %s", public)
	}
	history.RunID = "new-run"
	raw, _ = json.Marshal(buildHistoryMessages([]orm.ChatHistory{history}, nil))
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("old attempt replayed: %s", raw)
	}
}

func TestCanonicalDualAnswersRemainPrivateAndIndependent(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	body["run_id"], body["secondary_run_id"] = "primary-run", "secondary-run"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		run := request.Conversation.RunID
		emit := func(data map[string]any) { _ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": data}) }
		boundary := func(index int, phase string) {
			emit(map[string]any{"model_output": ChatModelOutput{RunID: run, CallIndex: index, Phase: phase}})
		}
		boundary(1, "begin")
		emit(map[string]any{"text": "answer"})
		emit(map[string]any{"canonical_messages": []map[string]any{{"role": "assistant", "content": "answer", "_lazyllm_provider_items": map[string]any{"protocol": "responses", "items": []any{map[string]any{"type": "reasoning", "encrypted_content": "SECRET-" + run}}}}}})
		boundary(1, "commit")
		boundary(2, "begin")
		emit(map[string]any{"canonical_messages": []map[string]any{{"role": "assistant", "content": "FAILED"}}})
		boundary(2, "rollback")
		emit(map[string]any{"runtime_event": runFinishedEvent(run, RunTerminal{Status: "failed", Reason: "model_failure", Code: "model_fallback_exhausted", PartialOutput: true})})
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamDualAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "question", "primary-history", "secondary-history", chatPersistTarget{HistoryID: "primary-history", Seq: 1}, nil)
	for _, id := range []string{"primary-history", "secondary-history"} {
		var row orm.MultiAnswersChatHistory
		if err := db.Where("id = ?", id).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		messages := canonicalMessagesFromHistory(orm.ChatHistory{RunID: row.RunID, Ext: row.Ext})
		raw, _ := json.Marshal(messages)
		if !strings.Contains(string(raw), "SECRET-"+row.RunID) || strings.Contains(string(raw), "FAILED") || len(messages) != 1 {
			t.Fatalf("wrong private replay for %s: %s", id, raw)
		}
	}
	if strings.Contains(recorder.Body.String(), "SECRET") || strings.Contains(recorder.Body.String(), "canonical_messages") {
		t.Fatal("private continuation leaked to dual SSE")
	}
}

func TestCanonicalFramesRejectMixedPublicContentAndBoundaries(t *testing.T) {
	messages := []map[string]any{{"role": "assistant", "content": "private"}}
	cases := []UpstreamStreamChunk{
		{CanonicalMessages: messages, Text: "public"},
		{CanonicalMessages: messages, Think: "reasoning"},
		{CanonicalMessages: messages, RuntimeEvent: runFinishedEvent("run", RunTerminal{Status: "completed"})},
	}
	for _, phase := range []string{"begin", "commit", "rollback"} {
		cases = append(cases, UpstreamStreamChunk{CanonicalMessages: messages, ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 1, Phase: phase}})
	}
	for _, chunk := range cases {
		if validateModelOutputChunk(chunk) == nil {
			t.Fatalf("mixed canonical frame accepted: %+v", chunk)
		}
	}
	if err := validateModelOutputChunk(UpstreamStreamChunk{CanonicalMessages: messages}); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalSteeringRetainsPartialBeforeFirstCompletedMessage(t *testing.T) {
	output := chatOutputProjection{text: "Partial answer"}
	output.appendCanonicalSteering([]steeringApplied{{ID: "steer", Text: "GUIDANCE"}})
	output.appendCanonical([]map[string]any{{"role": "assistant", "content": "Final"}})
	owner := newChatModelOutput("run")
	history := orm.ChatHistory{RunID: "run", RawContent: "original", Result: "Partial answerFinal", Ext: owner.ext(nil, output, "c", "h")}
	restored := canonicalMessagesFromHistory(history)
	if len(restored) != 3 || restored[0]["content"] != "Partial answer" || restored[1]["content"] != "GUIDANCE" || restored[2]["content"] != "Final" {
		t.Fatalf("steering was lost: %#v", restored)
	}
}
