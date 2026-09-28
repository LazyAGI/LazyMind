package taskcenter

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestNotificationReviewSessionLateUpdatePreservesTerminal(t *testing.T) {
	for _, terminal := range []string{"succeeded", "failed", "canceled", "skipped"} {
		for _, configured := range []bool{false, true} {
			for _, late := range []string{"failed", "waiting"} {
				t.Run(fmt.Sprintf("%s/configured=%v/late=%s", terminal, configured, late), func(t *testing.T) {
					db := orm.MigrateAllModelsForTest(t).DB
					sessionID, scheduleID := "session", "schedule"
					config := `{"events":{"failed":{"enabled":true,"content":"summary"}},"channels":{"desktop":{"enabled":true}}}`
					task := orm.TaskCenterTask{ID: "run", UserID: "owner", TaskType: "scheduled", ConversationID: "conv", Status: "running", WorkflowSessionID: &sessionID, ScheduleID: &scheduleID}
					if configured {
						task.NotificationConfig = &config
					}
					if err := db.Create(&task).Error; err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
					defer cancel()
					queried, resume := make(chan struct{}), make(chan struct{})
					var stopped atomic.Bool
					type barrierKey struct{}
					ctx = context.WithValue(ctx, barrierKey{}, true)
					if err := db.Callback().Query().After("gorm:query").Register("review:session-barrier", func(tx *gorm.DB) {
						if tx.Statement.Table != "task_center_tasks" || tx.Statement.Context.Value(barrierKey{}) != true || !stopped.CompareAndSwap(false, true) {
							return
						}
						close(queried)
						select {
						case <-resume:
						case <-ctx.Done():
							tx.AddError(ctx.Err())
						}
					}); err != nil {
						t.Fatal(err)
					}
					result := make(chan error, 1)
					go func() { result <- UpdateTaskStatusBySession(ctx, db, sessionID, late) }()
					select {
					case <-queried:
					case <-ctx.Done():
						t.Fatal("session query did not reach barrier")
					}
					finished := time.Now().UTC().Truncate(time.Second)
					// This is a separate database transaction/connection. SQLite WAL forces
					// the reader to retry its stale snapshot; PostgreSQL reaches the final
					// conditional UPDATE with its earlier selection of the running task.
					err := db.WithContext(ctx).Model(&orm.TaskCenterTask{}).Where("id = ?", task.ID).
						Updates(map[string]any{"status": terminal, "finished_at": finished}).Error
					close(resume)
					if err != nil {
						t.Fatal(err)
					}
					if err := <-result; err != nil {
						t.Fatal(err)
					}
					var saved orm.TaskCenterTask
					if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
						t.Fatal(err)
					}
					if saved.Status != terminal || saved.FinishedAt == nil || !saved.FinishedAt.Equal(finished) {
						t.Fatalf("late update overwrote terminal: %#v", saved)
					}
					var count int64
					if err := db.Model(&orm.TaskNotification{}).Count(&count).Error; err != nil {
						t.Fatal(err)
					}
					if count != 0 {
						t.Fatalf("illegal transition created %d notifications", count)
					}
				})
			}
		}
	}
}

func TestNotificationReviewRecoveryAdvancesPastBrokenAndActiveLegacyRuns(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	missing := "missing-session"
	now := time.Now().UTC()
	for i := 0; i < 101; i++ {
		task := orm.TaskCenterTask{ID: fmt.Sprintf("review-%03d", i), UserID: "owner", ConversationID: fmt.Sprintf("conv-%03d", i), TaskType: "scheduled", Status: "running", CreatedAt: now, UpdatedAt: now}
		if i == 0 {
			task.WorkflowSessionID = &missing
		}
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.ChatHistory{ID: "review-final", ConversationID: "conv-100", Seq: 1, RunStatus: "completed", Result: "最终结果"}).Error; err != nil {
		t.Fatal(err)
	}
	cursor, err := ReconcileScheduledNotifications(t.Context(), db, "", now)
	if err == nil || cursor != "review-099" {
		t.Fatalf("bad run hid error or blocked cursor: %q %v", cursor, err)
	}
	cursor, err = ReconcileScheduledNotifications(t.Context(), db, cursor, now)
	if err != nil || cursor != "" {
		t.Fatalf("final page: %q %v", cursor, err)
	}
	var task orm.TaskCenterTask
	if err := db.First(&task, "id = ?", "review-100").Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "succeeded" || task.NotificationConfig != nil {
		t.Fatalf("legacy recovery stalled or configured notifications: %#v", task)
	}
	var count int64
	if err := db.Model(&orm.TaskNotification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy recovery backfilled %d notices", count)
	}
}

func TestNotificationDispatchDoesNotWaitForRecovery(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	var once atomic.Bool
	if err := db.Callback().Query().Before("gorm:query").Register("review:slow-recovery", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_center_tasks" && once.CompareAndSwap(false, true) {
			close(entered)
			<-ctx.Done()
			tx.AddError(ctx.Err())
		}
	}); err != nil {
		t.Fatal(err)
	}
	// An invalid-target event still needs dispatch to settle its durable status;
	// the recovery scan is deliberately held until service cancellation.
	notice := orm.TaskNotification{ID: "ready", UserID: "owner", TaskID: "task", ScheduleID: "schedule", EventID: "event", Event: "succeeded", Channel: "wechat", Status: "pending", Content: "summary", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&notice).Error; err != nil {
		t.Fatal(err)
	}
	done := RunNotificationDelivery(ctx, db)
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := db.First(&notice, "id = ?", notice.ID).Error; err != nil {
			t.Fatal(err)
		}
		if notice.Status == "skipped" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ready notification was blocked behind recovery")
}
