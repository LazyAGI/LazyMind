package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrdinaryConversationEventWhitelist(t *testing.T) {
	for _, typ := range []string{"task_created", "task_updated", "artifact_created", "driver_input", "auto_chat_started", "workflow_step_feedback"} {
		input := &ConvEvent{Type: typ, Replayed: true, Payload: map[string]any{
			"task_id": "task-1", "session_id": "session-1", "status": "running",
			"history_id": "history-1",
			"objective":  "private-marker", "prompt": "private-marker", "message": "private-marker",
			"event":   map[string]any{"type": "think", "content": "private-marker"},
			"unknown": "private-marker",
		}}
		output := ordinaryConversationEvent(input)
		if output == nil || !output.Replayed {
			t.Fatalf("%s lost lifecycle notification", typ)
		}
		raw, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private-marker") {
			t.Fatalf("%s leaked private fields: %s", typ, raw)
		}
		if !strings.Contains(string(raw), "task-1") {
			t.Fatalf("%s lost task identity", typ)
		}
		if !strings.Contains(string(raw), `"history_id":"history-1"`) {
			t.Fatalf("%s lost history hydration identity", typ)
		}
	}
	if ordinaryConversationEvent(&ConvEvent{Type: "unregistered", Payload: map[string]any{"content": "private-marker"}}) != nil {
		t.Fatal("unknown events must not be forwarded in ordinary view")
	}
}
