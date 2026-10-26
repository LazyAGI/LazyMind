package subagent

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"testing"
)

func TestKnowledgeConfirmationEventReachesConversation(t *testing.T) {
	db := newTestDB(t)
	previous := EventHooks
	EventHooks = &eventHooks{}
	t.Cleanup(func() { EventHooks = previous })
	var delivered []map[string]any
	EventHooks.RegisterConversationEventHook(func(_ context.Context, _ state.Store, convID, historyID, eventType string, payload map[string]any) error {
		if convID != "conv" || historyID != "history" || eventType != "ask_pending" {
			t.Fatalf("wrong routing %s %s %s", convID, historyID, eventType)
		}
		delivered = append(delivered, payload)
		return nil
	})
	for _, agentType := range []string{"research", "workflow_step"} {
		task := &orm.SubAgentTask{ID: agentType, ConversationID: "conv", TriggerHistoryID: "history", AgentType: agentType, InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`), Params: json.RawMessage(`{}`)}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		var event TaskEvent
		if err := json.Unmarshal([]byte(`{"type":"ask_pending","task_id":"`+agentType+`","ask_pending":{"ask_id":"confirmation","knowledge_management":{"confirmation_id":"confirmation","arguments_digest":"digest","snapshot":"snapshot"}}}`), &event); err != nil {
			t.Fatal(err)
		}
		PublishConversationTaskEvent(context.Background(), db.DB, nil, event)
	}
	if len(delivered) != 2 {
		t.Fatalf("confirmation events lost: %#v", delivered)
	}
	if delivered[0]["ask_id"] != "confirmation" {
		t.Fatalf("card payload lost: %#v", delivered[0])
	}
}
