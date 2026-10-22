package taskcenter

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestOrdinaryRecoveryDoesNotCompleteNewTaskFromOlderHistory(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	now := time.Now().UTC()
	historyAt := now.Add(-time.Minute)
	tasks := []orm.TaskCenterTask{
		{ID: "older", UserID: "owner", ConversationID: "conversation", TaskType: "background_chat", Status: "running", CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "newer", UserID: "owner", ConversationID: "conversation", TaskType: "background_chat", Status: "running", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{ID: "history", ConversationID: "conversation", Seq: 1, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: historyAt, UpdateTime: historyAt}}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := RunNotificationDelivery(ctx, db)
	defer func() { cancel(); <-done }()
	var older orm.TaskCenterTask
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.First(&older, "id = ?", "older").Error; err != nil {
			t.Fatal(err)
		}
		if older.Status == "succeeded" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if older.Status != "succeeded" {
		t.Fatalf("eligible committed terminal history did not recover older task: %#v", older)
	}
	var newer orm.TaskCenterTask
	if err := db.First(&newer, "id = ?", "newer").Error; err != nil {
		t.Fatal(err)
	}
	if newer.Status != "running" || newer.FinishedAt != nil {
		t.Fatalf("older history completed newer task: %#v", newer)
	}
	var notices []orm.TaskNotification
	if err := db.Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].TaskID != older.ID || notices[0].Channel != "desktop" {
		t.Fatalf("wrong recovered notices: %#v", notices)
	}
}

func TestOrdinaryRecoveryUsesLatestSequenceAcrossSingleAndDualHistories(t *testing.T) {
	for _, latest := range []string{"single", "dual"} {
		t.Run(latest, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			now := time.Now().UTC()
			task := orm.TaskCenterTask{ID: "task", UserID: "owner", ConversationID: "conversation", TaskType: "background_chat", Status: "running", CreatedAt: now.Add(-time.Minute)}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			singleSeq, dualSeq, singleStatus, dualStatus := 2, 1, "generating", "completed"
			singleAt, dualAt := now.Add(-time.Second), now
			if latest == "dual" {
				singleSeq, dualSeq, singleStatus, dualStatus = 1, 2, "completed", "generating"
				singleAt, dualAt = now, now.Add(-time.Second)
			}
			if err := db.Create(&orm.ChatHistory{ID: "single", ConversationID: task.ConversationID, Seq: singleSeq, RunStatus: singleStatus,
				TimeMixin: orm.TimeMixin{CreateTime: singleAt, UpdateTime: singleAt}}).Error; err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"dual-primary", "dual-secondary"} {
				if err := db.Create(&orm.MultiAnswersChatHistory{ID: id, ConversationID: task.ConversationID, Seq: dualSeq, RunStatus: dualStatus,
					TimeMixin: orm.TimeMixin{CreateTime: dualAt, UpdateTime: dualAt}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
				t.Fatal(err)
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != "running" || task.FinishedAt != nil {
				t.Fatalf("older %s reply completed an active newer sequence: %#v", map[string]string{"single": "dual", "dual": "single"}[latest], task)
			}
		})
	}
}

func TestOrdinaryWorkflowRecoveryRechecksSessionAfterConcurrentRetry(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	if db.Dialector.Name() != "sqlite" {
		t.Skip("SQLite snapshot retry regression; PostgreSQL uses a session row lock")
	}
	sessionID := "session"
	for _, row := range []any{
		&orm.WorkflowSession{ID: sessionID, CreateUserID: "owner", Status: "failed"},
		&orm.TaskCenterTask{ID: "task", UserID: "owner", TaskType: "workflow_run", Status: "running", WorkflowSessionID: &sessionID},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	queried, resume := make(chan struct{}), make(chan struct{})
	var stopped atomic.Bool
	if err := db.Callback().Query().After("gorm:query").Register("notification:recovery-retry-barrier", func(tx *gorm.DB) {
		if tx.Statement.Table != "plugin_sessions" || !stopped.CompareAndSwap(false, true) {
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
	go func() { _, err := ReconcileOrdinaryNotifications(ctx, db, ""); result <- err }()
	select {
	case <-queried:
	case <-ctx.Done():
		t.Fatal("recovery did not reach session snapshot")
	}
	err := db.WithContext(ctx).Model(&orm.WorkflowSession{}).Where("id = ?", sessionID).Update("status", "active").Error
	close(resume)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	var task orm.TaskCenterTask
	if err := db.First(&task, "id = ?", "task").Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.FinishedAt != nil {
		t.Fatalf("stale failed session overwrote accepted retry: %#v", task)
	}
	var count int64
	if err := db.Model(&orm.TaskNotification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale recovery created %d notices", count)
	}
}
