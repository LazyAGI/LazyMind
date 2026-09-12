package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestConversationBatchStatusRetainsLatestTerminalResult(t *testing.T) {
	db, cache := runningStatusDB(t, "completed", "failed", "canceled", "latest", "waiting", "background")
	now := time.Now().UTC()
	for _, status := range []string{"completed", "failed", "cancelled"} {
		id := status
		if status == "cancelled" {
			id = "canceled"
		}
		seedRunningRecord(t, db, &orm.MultiAnswersChatHistory{ID: "history-" + id, ConversationID: id, Seq: 1, RunID: "run-" + id, RunStatus: status, TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}})
	}
	seedRunningRecord(t, db, &orm.MultiAnswersChatHistory{ID: "old", ConversationID: "latest", Seq: 1, RunStatus: "failed", TimeMixin: orm.TimeMixin{CreateTime: now.Add(-time.Hour), UpdateTime: now}})
	seedRunningRecord(t, db, &orm.MultiAnswersChatHistory{ID: "new", ConversationID: "latest", Seq: 2, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}})
	seedRunningRecord(t, db, &orm.WorkflowSession{ID: "old-workflow", ConversationID: "latest", CreateUserID: "u1", TriggerHistoryID: "old", Status: "failed", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(time.Second)})
	seedRunningRecord(t, db, &orm.MultiAnswersChatHistory{ID: "waiting-history", ConversationID: "waiting", Seq: 1, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}})
	seedRunningRecord(t, db, &orm.WorkflowSession{ID: "waiting-workflow", ConversationID: "waiting", CreateUserID: "u1", TriggerHistoryID: "waiting-history", Status: "waiting", CreatedAt: now, UpdatedAt: now})
	seedRunningRecord(t, db, &orm.MultiAnswersChatHistory{ID: "background-history", ConversationID: "background", Seq: 1, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}})
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "active-child", ConversationID: "background", CreateUserID: "u1", TriggerHistoryID: "background-history", AgentType: "research", Status: "running", CreatedAt: now})
	assertTerminal := func(want map[string]string) {
		t.Helper()
		ids := []string{}
		for id := range want {
			ids = append(ids, id)
		}
		rows, err := batchConversationRunningStatus(context.Background(), db, cache, "u1", ids, now)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(rows)
		var payload []map[string]any
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatal(err)
		}
		for _, row := range payload {
			id := row["conversation_id"].(string)
			got, _ := row["terminal_status"].(string)
			version, _ := row["terminal_version"].(string)
			if (got == "") != (version == "") {
				t.Errorf("terminal version presence mismatch: %v", row)
			}
			if got != want[id] {
				t.Errorf("%s terminal=%q want=%q", id, got, want[id])
			}
		}
	}
	assertTerminal(map[string]string{"completed": "completed", "failed": "failed", "canceled": "canceled", "latest": "completed", "waiting": "", "background": ""})
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "active-child").Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	assertTerminal(map[string]string{"background": "failed"})
	// A newer run without a durable terminal must not reuse an older success.
	seedRunningRecord(t, db, &orm.ChatHistory{ID: "new-unknown", ConversationID: "latest", Seq: 3, RunStatus: "", TimeMixin: orm.TimeMixin{CreateTime: now.Add(time.Second), UpdateTime: now}})
	assertTerminal(map[string]string{"latest": ""})
	seedRunningRecord(t, db, &orm.WorkflowSessionStep{ID: "stopped-attempt", SessionID: "waiting-workflow", StepID: "step", TaskID: "stopped-task", Attempt: 1, Status: "interrupted", TerminalCode: "WORKFLOW_STOPPED", CreatedAt: now, UpdatedAt: now})
	assertTerminal(map[string]string{"waiting": "canceled"})
	seedRunningRecord(t, db, &orm.WorkflowSessionStep{ID: "retried-attempt", SessionID: "waiting-workflow", StepID: "step", TaskID: "retry-task", Attempt: 2, Status: "succeeded", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)})
	assertTerminal(map[string]string{"waiting": ""})
	// A missing activity source must never turn a known old result into success.
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	assertTerminal(map[string]string{"completed": "", "failed": "", "canceled": ""})
}

func TestConversationTerminalStatusesRespectVisibility(t *testing.T) {
	db, cache := runningStatusDB(t, "mine", "foreign")
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "foreign").Update("create_user_id", "other-user").Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mine", "foreign"} {
		seedRunningRecord(t, db, &orm.ChatHistory{ID: "history-" + id, ConversationID: id, Seq: 1, RunStatus: "failed"})
	}
	rows, err := batchConversationRunningStatus(t.Context(), db, cache, "u1", []string{"mine", "foreign", "missing"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ConversationID != "mine" || rows[0].TerminalStatus != "failed" {
		t.Fatalf("unexpected visible terminal statuses: %#v", rows)
	}
}

func TestConversationTerminalVersionIdentifiesNewResults(t *testing.T) {
	db, cache := runningStatusDB(t, "a")
	now := time.Now().UTC()
	seedRunningRecord(t, db, &orm.ChatHistory{ID: "reply", ConversationID: "a", Seq: 1, RunID: "run-1", RunStatus: "completed"})
	version := func() string {
		t.Helper()
		rows, err := batchConversationRunningStatus(t.Context(), db, cache, "u1", []string{"a"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].TerminalVersion == "" {
			t.Fatalf("missing terminal version: %#v", rows)
		}
		return rows[0].TerminalVersion
	}
	first := version()
	if version() != first {
		t.Fatal("polling changed version")
	}
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "reply").Update("update_time", now).Error; err != nil {
		t.Fatal(err)
	}
	if version() != first {
		t.Fatal("unrelated reply edits changed result version")
	}
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "reply").Update("run_id", "run-2").Error; err != nil {
		t.Fatal(err)
	}
	if version() == first {
		t.Fatal("regenerated reply reused read receipt")
	}
	seedRunningRecord(t, db, &orm.WorkflowSession{ID: "workflow", ConversationID: "a", CreateUserID: "u1", TriggerHistoryID: "reply", Status: "completed", CreatedAt: now, UpdatedAt: now})
	workflow := version()
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "workflow").Update("updated_at", now.Add(time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if version() == workflow {
		t.Fatal("new workflow result reused read receipt")
	}
	beforeChild := version()
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "child", ConversationID: "a", CreateUserID: "u1", TriggerHistoryID: "reply", AgentType: "research", Status: "completed", CreatedAt: now, UpdatedAt: now})
	if version() == beforeChild {
		t.Fatal("background completion reused parent receipt")
	}
}
