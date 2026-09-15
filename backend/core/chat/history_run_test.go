package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestOwnedChatHistoryRejectsLateRunWrite(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	ctx := context.Background()
	now := time.Now()
	if err := db.Create(&orm.ChatHistory{
		ID: "history-owned", ConversationID: "conversation-1", Seq: 1,
		RunID: "run-old", RunStatus: "generating",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}
	if err := claimChatHistoryRun(ctx, db, "history-owned", "run-new"); err != nil {
		t.Fatalf("claim new run: %v", err)
	}
	if updated, err := updateOwnedChatHistory(ctx, db, "history-owned", "run-old", map[string]any{
		"run_status": "failed",
	}); err != nil || updated {
		t.Fatalf("late old run update: updated=%v err=%v", updated, err)
	}
	if updated, err := updateOwnedChatHistory(ctx, db, "history-owned", "run-new", map[string]any{
		"run_status": "completed",
	}); err != nil || !updated {
		t.Fatalf("current run update: updated=%v err=%v", updated, err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", "history-owned").Error; err != nil {
		t.Fatalf("load history: %v", err)
	}
	if history.RunID != "run-new" || history.RunStatus != "completed" {
		t.Fatalf("history owner changed unexpectedly: %#v", history)
	}
}

func TestChatStatusRejectsLateRunTerminal(t *testing.T) {
	ctx := context.Background()
	stateStore := newRunDecisionTestStore(t)
	if err := setChatRuntimeStatus(ctx, stateStore, "conversation-1", "history-1", "generating", "", "run-new", nil); err != nil {
		t.Fatalf("set current run: %v", err)
	}
	late := &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "transport_error"}
	if err := setChatRuntimeStatus(ctx, stateStore, "conversation-1", "history-1", "failed", "old", "run-old", late); err != nil {
		t.Fatalf("set stale terminal: %v", err)
	}
	status, err := getChatStatus(ctx, stateStore, "conversation-1", "history-1")
	if err != nil {
		t.Fatalf("get current status: %v", err)
	}
	if status.RunID != "run-new" || status.Status != "generating" || status.RunTerminal != nil {
		t.Fatalf("stale terminal overwrote current status: %#v", status)
	}
}

func TestExternalRegenerationOwnerRejectsLatePreviousTerminal(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	ctx := context.Background()
	now := time.Now().UTC()
	oldRun := &orm.ExternalChatRun{
		ID: "external-old", RequestID: "external-old", ConversationID: "conversation-1",
		HistoryID: "shared-history", Provider: ChatExecutorCodex, ActorUserID: "user-1",
		Action: "start", Query: "old", Sequence: 1,
	}
	if err := app.createRun(ctx, oldRun); err != nil {
		t.Fatalf("create old run: %v", err)
	}
	job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil || job.RunID != oldRun.ID {
		t.Fatalf("claim old run: job=%#v err=%v", job, err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID: oldRun.HistoryID, ConversationID: oldRun.ConversationID, Seq: 1,
		RunID: oldRun.ID, RunStatus: "generating",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create old history: %v", err)
	}
	newRun := &orm.ExternalChatRun{
		ID: "external-new", RequestID: "external-new", ConversationID: "conversation-1",
		HistoryID: oldRun.HistoryID, Provider: ChatExecutorCodex, ActorUserID: "user-1",
		Action: "regenerate", Query: "new", Sequence: 1,
	}
	if err := app.createRun(ctx, newRun); err != nil {
		t.Fatalf("create regenerated run: %v", err)
	}
	if _, err := app.appendEvent(ctx, "user-1", oldRun.ID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "old-failed", Type: "failed", Error: "late failure"}); err != nil {
		t.Fatalf("append late old terminal: %v", err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", oldRun.HistoryID).Error; err != nil {
		t.Fatalf("load current history: %v", err)
	}
	if history.RunID != newRun.ID || history.RunStatus != "generating" {
		t.Fatalf("late old terminal overwrote regeneration: %#v", history)
	}
	if err := app.requestStop(ctx, "user-1", "conversation-1", oldRun.HistoryID); err != nil {
		t.Fatalf("stop current external run: %v", err)
	}
	if err := db.First(&history, "id = ?", oldRun.HistoryID).Error; err != nil {
		t.Fatalf("reload current history: %v", err)
	}
	var terminal RunTerminal
	if err := json.Unmarshal(history.RunTerminal, &terminal); err != nil {
		t.Fatalf("decode current terminal: %v", err)
	}
	if history.RunID != newRun.ID || terminal.Status != "cancelled" || terminal.Reason != "user_cancelled" {
		t.Fatalf("current external terminal mismatch: history=%#v terminal=%#v", history, terminal)
	}
}
