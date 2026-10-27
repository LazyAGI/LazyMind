package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestChatCapacitySwitchCommitsAnswerAndSelection(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	state := body[chatModelFallbackBodyKey].(*chatFallbackState)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		run := request.Conversation.RunID
		encoder := json.NewEncoder(w)
		emit := func(data map[string]any) { _ = encoder.Encode(map[string]any{"code": 200, "data": data}) }
		emit(map[string]any{"model_output": ChatModelOutput{RunID: run, CallIndex: 1, Phase: "begin"}})
		emit(map[string]any{"text": "answer from fitting model", "model_output": ChatModelOutput{RunID: run, CallIndex: 1}})
		emit(map[string]any{
			"model_output": ChatModelOutput{RunID: run, CallIndex: 1, Phase: "commit"},
			"runtime_event": fallbackWireEvent(run, "capacity-switch", RuntimeEventModelSwitched, ModelSwitchedData{
				SwitchIndex: 1, From: state.identity("own:model-a"), To: state.identity("own:model-b"),
				ReasonCode: "model_context_not_fit",
			}),
		})
		emit(map[string]any{"runtime_event": runFinishedEvent(run, RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true})})
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	target := chatPersistTarget{HistoryID: "capacity-history", Seq: 1}
	ext := mergeChatModelRouteIntoExt(nil, body)
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body,
		conversation.ID, "continue with this context", target.HistoryID, target, ext)
	if !strings.Contains(recorder.Body.String(), `"status":"completed"`) || strings.Contains(recorder.Body.String(), "protocol_error") {
		t.Fatalf("client did not receive a successful terminal: %s", recorder.Body.String())
	}
	var history orm.ChatHistory
	if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	if history.RunStatus != "completed" || history.Result != "answer from fitting model" {
		t.Fatalf("capacity switch lost the completed answer: status=%s terminal=%s result=%q", history.RunStatus, history.RunTerminal, history.Result)
	}
	records := modelSwitchesFromExt(history.Ext)
	if len(records) != 1 || records[0].ReasonCode != "model_context_not_fit" || records[0].To.ModelID != "model-b" {
		t.Fatalf("capacity switch was not retained in history: %#v", records)
	}
	var stored orm.Conversation
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ChatModelID == nil || *stored.ChatModelID != "model-b" {
		t.Fatal("capacity switch did not update the conversation selection")
	}
}
