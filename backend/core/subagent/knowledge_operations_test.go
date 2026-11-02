package subagent

import (
	"encoding/json"
	"errors"
	"lazymind/core/common/orm"
	"testing"
)

func TestKnowledgeReceiptsPersistDeduplicateAndFenceExecution(t *testing.T) {
	db := newTestDB(t)
	task, err := CreateTask(t.Context(), db.DB, CreateTaskInput{TaskID: "receipt-task", ConversationID: "conversation", CreateUserID: "alice", AgentType: "research"})
	if err != nil {
		t.Fatal(err)
	}
	ev := TaskEvent{Type: "knowledge_operation_result", TaskID: task.ID, ExecutionID: task.ExecutionID,
		KnowledgeOperation: json.RawMessage(`{"call_id":"call","action":"knowledge.create","status":"success","summary":{"total":1,"succeeded":1},"items":[{"name":"Library","resource_ref":{"kind":"knowledge_base","knowledge_base_id":"kb"},"resource_status":"created"}]}`)}
	accepted, err := persistTaskEvent(t.Context(), db.DB, ev)
	if err != nil || !accepted {
		t.Fatalf("receipt not saved: %v %v", accepted, err)
	}
	accepted, err = persistTaskEvent(t.Context(), db.DB, ev)
	if err != nil || accepted {
		t.Fatalf("receipt not deduplicated: %v %v", accepted, err)
	}
	fresh, _ := GetTask(t.Context(), db.DB, task.ID)
	view, err := OrdinaryTask(t.Context(), db.DB, fresh)
	if err != nil || len(view.KnowledgeOperations) != 1 || view.KnowledgeOperations[0].CallID != "call" || view.Pages.KnowledgeOperations.Total != 1 {
		t.Fatalf("history missing receipt: %+v %v", view.KnowledgeOperations, err)
	}
	ev.ExecutionID = "old-execution"
	if _, err = persistTaskEvent(t.Context(), db.DB, ev); !errors.Is(err, ErrStaleExecution) {
		t.Fatalf("stale event accepted: %v", err)
	}
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", task.ID).Update("execution_id", "next").Error; err != nil {
		t.Fatal(err)
	}
	fresh, _ = GetTask(t.Context(), db.DB, task.ID)
	view, err = OrdinaryTask(t.Context(), db.DB, fresh)
	if err != nil || len(view.KnowledgeOperations) != 0 {
		t.Fatalf("previous execution leaked: %v", err)
	}
}
