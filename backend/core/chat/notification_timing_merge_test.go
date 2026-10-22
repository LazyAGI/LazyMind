package chat

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
	"lazymind/core/tasktiming"
)

func TestChatTimingAndBackgroundNotificationKeepTerminalAcrossReplayAndRegeneration(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	if err := db.AutoMigrate(&orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	ctx := tasktiming.WithRequestTime(t.Context(), start)
	task := scopedBackgroundTestTask(t, db, "background", "history", "run")
	if err := registerForkableHistoryRun(ctx, db, "conversation-1", "history", "run", "question", chatPersistTarget{Seq: 1}, nil); err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	finish := func(at time.Time) {
		t.Helper()
		updated, err := updateOwnedChatHistory(ctx, db, "history", "run", map[string]any{
			"run_status": "completed", "update_time": at,
		})
		if err != nil || !updated {
			t.Fatalf("terminal update: updated=%v err=%v", updated, err)
		}
		if err := taskcenter.FinalizeBackgroundExecution(ctx, db, "conversation-1", "history", "run"); err != nil {
			t.Fatal(err)
		}
	}
	finish(completedAt)
	var originalTiming orm.TaskExecutionTiming
	if err := db.First(&originalTiming, "id = ?", "run").Error; err != nil {
		t.Fatal(err)
	}
	if originalTiming.Status != "completed" || originalTiming.FinishedAt == nil || !originalTiming.RequestedAt.Equal(backgroundStoredCompletionTime(db, start)) {
		t.Fatalf("notification terminal did not settle request timing: %#v", originalTiming)
	}
	finish(completedAt.Add(time.Hour))
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "succeeded" || task.FinishedAt == nil || !task.FinishedAt.Equal(backgroundStoredCompletionTime(db, completedAt)) {
		t.Fatalf("replay changed notification completion: %#v", task)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", "history").Error; err != nil {
		t.Fatal(err)
	}
	if err := registerForkableHistoryRun(ctx, db, "conversation-1", "history", "replacement", "retry", chatPersistTarget{Seq: 1, IsRegeneration: true, Existing: &history}, nil); err != nil {
		t.Fatal(err)
	}
	updated, err := updateOwnedChatHistory(ctx, db, "history", "run", map[string]any{"run_status": "failed"})
	if err != nil || updated {
		t.Fatalf("stale terminal crossed regeneration: updated=%v err=%v", updated, err)
	}
	var currentTiming, replacement orm.TaskExecutionTiming
	if err := db.First(&currentTiming, "id = ?", "run").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&replacement, "id = ?", "replacement").Error; err != nil {
		t.Fatal(err)
	}
	if currentTiming.FinishedAt == nil || !currentTiming.FinishedAt.Equal(*originalTiming.FinishedAt) || replacement.FinishedAt != nil {
		t.Fatalf("replay or stale terminal changed timing: original=%#v replacement=%#v", currentTiming, replacement)
	}
	var notices []orm.TaskNotification
	if err := db.Where("task_id = ?", task.ID).Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Event != "succeeded" {
		t.Fatalf("terminal replay duplicated or replaced the notification: %#v", notices)
	}
}
