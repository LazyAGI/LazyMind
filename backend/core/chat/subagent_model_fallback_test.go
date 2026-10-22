package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestChatModelFallbackNewChildInheritsConfirmedModelWithoutChangingOldChild(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	received := make(chan map[string]any, 2)
	releaseOld := make(chan struct{})
	finished := make(chan string, 2)
	child := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		received <- request
		id := request["task_id"].(string)
		if id == "old-child" {
			<-releaseOld
		}
		run := request["task_spec"].(map[string]any)["execution_id"].(string)
		fmt.Fprintf(w, "data: {\"type\":\"done\",\"execution_id\":%q,\"summary\":\"ok\"}\n\n", run)
		finished <- id
	}))
	defer child.Close()
	defer close(releaseOld)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", child.URL)
	switched := hostFallbackSwitch(body, "run-fallback")
	parent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		enc := json.NewEncoder(w)
		emit := func(data map[string]any) { _ = enc.Encode(map[string]any{"code": 200, "data": data}) }
		task := func(id string) {
			emit(map[string]any{"task_created": map[string]any{"task_id": id, "agent_type": "research", "title": id, "objective": "read", "params": map[string]any{}}})
		}
		model := func(index int, phase string, data map[string]any) {
			data["model_output"] = &ChatModelOutput{RunID: request.Conversation.RunID, CallIndex: index, Phase: phase}
			emit(data)
		}
		model(1, "begin", map[string]any{})
		model(1, "commit", map[string]any{})
		task("old-child")
		model(2, "begin", map[string]any{})
		model(2, "", map[string]any{"text": "from B"})
		model(2, "commit", map[string]any{"runtime_event": switched})
		task("new-child")
		emit(map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "completed", Reason: "normal"})})
	}))
	defer parent.Close()
	recorder := httptest.NewRecorder()
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, newRunDecisionTestStore(t), parent.URL, body, conversation.ID, "hello", "parent-child-model", chatPersistTarget{Seq: 1}, mergeChatModelRouteIntoExt(nil, body))
	requests := map[string]map[string]any{}
	for range 2 {
		select {
		case request := <-received:
			requests[request["task_id"].(string)] = request
		case <-time.After(5 * time.Second):
			t.Fatalf("child run did not receive Core input: %s", recorder.Body.String())
		}
	}
	for id, expected := range map[string]string{"old-child": "model-a", "new-child": "model-b"} {
		request := requests[id]
		if request["model_fallback_current_id"] != "own:"+expected || request["llm_config"].(map[string]any)["llm"].(map[string]any)["model"] != "gpt-"+expected[len("model-"):] {
			t.Fatalf("%s inherited wrong effective model: %#v", id, request)
		}
		if request["model_fallback_initial_switch"] != nil {
			t.Fatal("healthy inherited model was labeled a recovered switch")
		}
	}
	requests["new-child"]["llm_config"].(map[string]any)["llm"].(map[string]any)["model"] = "changed"
	if requests["old-child"]["llm_config"].(map[string]any)["llm"].(map[string]any)["model"] != "gpt-a" {
		t.Fatal("new child mutated already running child's runtime config")
	}
	var stored orm.Conversation
	_ = db.Where("id = ?", conversation.ID).Take(&stored).Error
	var snapshot chatModelSnapshot
	_ = json.Unmarshal(stored.ChatModelSnapshot, &snapshot)
	if stored.ChatModelID == nil || *stored.ChatModelID != "model-b" || snapshot.ModelID != "model-b" {
		t.Fatal("parent selection did not follow its confirmed model B")
	}
	// Await both actual consumers before the shared database fixture closes.
	releaseOld <- struct{}{}
	for range 2 {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("child did not finish")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int64
		_ = db.Model(&orm.SubAgentTask{}).Where("id IN ? AND status = ?", []string{"old-child", "new-child"}, "succeeded").Count(&count).Error
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child terminal was not committed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
