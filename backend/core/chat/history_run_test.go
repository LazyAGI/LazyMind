package chat

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestOwnedChatHistoryRejectsLateRunWrite(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Minute)
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
	var claimed orm.ChatHistory
	if err := db.First(&claimed, "id = ?", "history-owned").Error; err != nil {
		t.Fatalf("load claimed history: %v", err)
	}
	if !claimed.CreateTime.After(now) {
		t.Fatalf("regeneration did not refresh create_time: %v", claimed.CreateTime)
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

func TestOwnedChatHistoryPreservesKnowledgeDecisionDuringTerminalSave(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	stored := json.RawMessage(`{"ask_pending":{"ask_id":"confirm-current","knowledge_management":{"confirmation_id":"confirm-current"}},"ask_answered":true,"ask_saved_answers":{"0":{"type":"boolean","value":"__ask_user_no__"}}}`)
	if err := db.Create(&orm.ChatHistory{ID: "knowledge-history", ConversationID: "conversation-1", RunID: "knowledge-run", Ext: stored}).Error; err != nil {
		t.Fatal(err)
	}
	for _, incoming := range []string{
		`{"ask_pending":{"ask_id":"confirm-current","knowledge_management":{"confirmation_id":"confirm-current"}},"ask_answered":false,"terminal":"saved"}`,
		`{"ask_pending":{"ask_id":"older-confirm","knowledge_management":{"confirmation_id":"older-confirm"}},"terminal":"saved"}`,
		`{"terminal":"saved"}`,
	} {
		updated, err := updateOwnedChatHistory(context.Background(), db, "knowledge-history", "knowledge-run", map[string]any{"ext": json.RawMessage(incoming), "run_status": "completed"})
		if err != nil || !updated {
			t.Fatalf("save: %v %v", updated, err)
		}
		var history orm.ChatHistory
		if err := db.First(&history, "id = ?", "knowledge-history").Error; err != nil {
			t.Fatal(err)
		}
		var ext map[string]any
		if err := json.Unmarshal(history.Ext, &ext); err != nil {
			t.Fatal(err)
		}
		pending, _ := ext["ask_pending"].(map[string]any)
		if ext["ask_answered"] != true || pending["ask_id"] != "confirm-current" || ext["ask_saved_answers"] == nil || ext["terminal"] != "saved" {
			t.Fatalf("decision overwritten: %s", history.Ext)
		}
	}
}

func TestFallbackProgressPreservesKnowledgeConfirmationThroughResume(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answered bool
		incoming string
	}{
		{"answered_card", true, `{"ask_pending":{"ask_id":"confirm-current","knowledge_management":{"confirmation_id":"confirm-current"}},"ask_answered":false}`},
		{"answered_card_missing_from_snapshot", true, `{}`},
		{"new_pending_card", false, `{"ask_pending":{"ask_id":"older-confirm","knowledge_management":{"confirmation_id":"older-confirm"}},"ask_answered":true,"ask_saved_answers":{"0":"old"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, db := newExternalChatTestApplication(t)
			if err := db.AutoMigrate(&orm.ChatRunPerformance{}); err != nil {
				t.Fatal(err)
			}
			stored := map[string]any{"ask_pending": map[string]any{"ask_id": "confirm-current", "knowledge_management": map[string]any{"confirmation_id": "confirm-current"}}}
			if tc.answered {
				stored["ask_answered"] = true
				stored["ask_saved_answers"] = map[string]any{"0": map[string]any{"type": "boolean", "value": "__ask_user_no__"}}
			}
			ext, _ := json.Marshal(stored)
			if err := db.Create(&orm.ChatHistory{ID: "knowledge-progress", ConversationID: "conversation-1", RunID: "knowledge-run", RunStatus: "generating", Ext: ext}).Error; err != nil {
				t.Fatal(err)
			}
			checkConfirmation := func(fields map[string]any) {
				t.Helper()
				pending, _ := fields["ask_pending"].(map[string]any)
				answered, _ := fields["ask_answered"].(bool)
				if pending["ask_id"] != "confirm-current" || answered != tc.answered || (fields["ask_saved_answers"] != nil) != tc.answered {
					t.Fatalf("saved confirmation overwritten: %+v", fields)
				}
				if tc.answered {
					answers := fields["ask_saved_answers"].(map[string]any)
					if answers["0"].(map[string]any)["value"] != "__ask_user_no__" {
						t.Fatalf("saved answer changed: %+v", answers)
					}
				}
			}
			stale := json.RawMessage(tc.incoming)
			if err := persistChatFallbackProgress(t.Context(), db, "conversation-1", "knowledge-progress", "knowledge-run", "query", "", "committed output", 1, stale, nil, nil); err != nil {
				t.Fatal(err)
			}
			var history orm.ChatHistory
			if err := db.First(&history, "id = ?", "knowledge-progress").Error; err != nil {
				t.Fatal(err)
			}
			if history.Result != "committed output" {
				t.Fatal("model progress was not persisted")
			}
			checkConfirmation(chatHistoryToResponseItem(history))
			terminal, _ := completedRunEvent("knowledge-run", true).Terminal()
			terminalJSON, _ := json.Marshal(terminal)
			updated, err := updateOwnedChatHistory(t.Context(), db, history.ID, history.RunID, map[string]any{"ext": stale, "run_status": "completed", "run_terminal": json.RawMessage(terminalJSON)})
			if err != nil || !updated {
				t.Fatalf("terminal save: updated=%v err=%v", updated, err)
			}
			for _, completed := range []bool{false, true} {
				recorder := httptest.NewRecorder()
				if completed {
					resumeCompletedFromDB(t.Context(), db, "user-1", "conversation-1", recorder, recorder)
				} else {
					resumeFromDBOnly(t.Context(), db, "user-1", "conversation-1", recorder, recorder)
				}
				var snapshot struct {
					Result map[string]any `json:"result"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.Split(recorder.Body.String(), "\n")[0], "data: ")), &snapshot); err != nil {
					t.Fatal(err)
				}
				checkConfirmation(snapshot.Result)
			}
		})
	}
}
