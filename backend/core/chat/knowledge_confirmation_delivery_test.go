package chat

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/subagent"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkConfirmationProductionHookPreservesPublicIdentity(t *testing.T) {
	db := orm.OpenTestDB(t)
	if e := db.AutoMigrate(&orm.SubAgentTask{}); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	ss, e := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	hook := PublishConversationEvent
	subagent.EventHooks.RegisterConversationEventHook(hook)
	task := orm.SubAgentTask{ID: "work", ConversationID: "conv", TriggerHistoryID: "history", AgentType: "research", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`), Params: json.RawMessage(`{}`)}
	if e := db.Create(&task).Error; e != nil {
		t.Fatal(e)
	}
	pending := map[string]any{"ask_id": "confirmation", "questions": []any{map[string]any{"text": "Delete?", "type": "boolean"}}, "knowledge_management": map[string]any{"confirmation_id": "confirmation", "arguments_digest": "digest", "snapshot": "snapshot"}}
	subagent.PublishConversationTaskEvent(ctx, db.DB, ss, subagent.TaskEvent{TaskID: task.ID, Type: "ask_pending", AskPending: pending})
	raw, e := ss.LRange(ctx, convEventsKey("conv"), 0, -1)
	if e != nil || len(raw) != 1 {
		t.Fatalf("events=%d error=%v", len(raw), e)
	}
	var event ConvEvent
	if e = json.Unmarshal([]byte(raw[0]), &event); e != nil {
		t.Fatal(e)
	}
	public := ordinaryConversationEvent(&event)
	body, _ := json.Marshal(public)
	t.Logf("actual hook output=%s ordinary wire=%s", raw[0], body)
	m := public.Payload.(map[string]any)
	if m["history_id"] != "history" || m["confirmation_id"] != "confirmation" || m["ask_id"] != nil || m["knowledge_management"] != nil {
		t.Fatalf("public confirmation identity incorrect: %+v", m)
	}
	control := ordinaryConversationEvent(&ConvEvent{Type: "ask_pending", Payload: map[string]any{"conversation_id": "conv", "history_id": "history"}})
	if control.Payload.(map[string]any)["history_id"] != "history" {
		t.Fatal("control history hydration identity lost")
	}
	t.Logf("control when hook supplies history_id: %+v", control.Payload)
}

func TestCompletedResumeRestoresSavedConfirmation(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{}, &orm.ChatRunPerformance{})
	row := orm.ChatHistory{ID: "history", ConversationID: "conv", Seq: 1, Result: "Main answer completed", RunID: "run", RunStatus: "completed", Ext: json.RawMessage(`{"ask_pending":{"ask_id":"confirmation","questions":[{"text":"Delete?","type":"boolean"}],"knowledge_management":{"confirmation_id":"confirmation","arguments_digest":"digest","snapshot":"snapshot"}}}`)}
	terminal, e := completedRunEvent("run", true).Terminal()
	if e != nil {
		t.Fatal(e)
	}
	row.RunTerminal, e = json.Marshal(terminal)
	if e != nil {
		t.Fatal(e)
	}
	if e := db.Create(&row).Error; e != nil {
		t.Fatal(e)
	}
	for _, completed := range []bool{false, true} {
		rec := httptest.NewRecorder()
		if completed {
			resumeCompletedFromDB(context.Background(), db.DB, "alice", "conv", rec, rec)
		} else {
			resumeFromDBOnly(context.Background(), db.DB, "alice", "conv", rec, rec)
		}
		if !strings.Contains(string(row.Ext), "ask_pending") {
			t.Fatal("invalid fixture")
		}
		t.Logf("completed=%t persisted_ask=true resume_contains_ask=%t wire=%s", completed, strings.Contains(rec.Body.String(), "ask_pending"), rec.Body.String())
		if !strings.Contains(rec.Body.String(), "ask_pending") {
			t.Fatal("saved confirmation lost during resume")
		}
	}
}
