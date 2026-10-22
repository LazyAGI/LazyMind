package taskcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

func scheduledRecoveryFenceFixture(t *testing.T, state string) (*gorm.DB, orm.WorkflowSession, orm.TaskCenterTask) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.ChatHistory{},
		&orm.TaskCenterTask{}, &orm.TaskNotification{}, &orm.UserNotificationPreferences{}, &orm.TaskRunOutput{},
		&orm.ConversationArtifact{}, &orm.SubAgentTask{}, &orm.SubAgentArtifact{}).DB
	now := time.Now().UTC().Truncate(time.Microsecond)
	session := orm.WorkflowSession{ID: "session", ConversationID: "conversation", CreateUserID: "owner", Status: state,
		CurrentStepID: "write", WorkflowMode: "dynamic", StateVersion: 3, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}
	scheduleID := "schedule"
	config := DefaultNotificationConfig()
	config.Events["succeeded"] = NotificationEventRule{Enabled: true, Content: "full"}
	config.Events["waiting"] = NotificationEventRule{Enabled: true, Content: "summary"}
	raw, _ := json.Marshal(config)
	configJSON := string(raw)
	task := orm.TaskCenterTask{ID: "task", UserID: "owner", ConversationID: session.ConversationID,
		TaskType: "scheduled", WorkflowSessionID: &session.ID, ScheduleID: &scheduleID,
		Status: "running", NotificationConfig: &configJSON, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}
	for _, row := range []any{&session, &task,
		&orm.WorkflowSessionStep{ID: "step", SessionID: session.ID, StepID: "write", Attempt: 1, Status: "interrupted", Validity: "effective"},
		&orm.ChatHistory{ID: "history", ConversationID: task.ConversationID, Seq: 1, RunStatus: "completed", Result: "initial completed result",
			TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, session, task
}

func acceptScheduledRecoveryRetry(ctx context.Context, db *gorm.DB, session orm.WorkflowSession, task orm.TaskCenterTask, complete bool) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", session.ID).Error; err != nil {
			return err
		}
		state, version := "active", locked.StateVersion+1
		if complete {
			state, version = "completed", version+1
		}
		if err := tx.Model(&orm.WorkflowSession{}).Where("id = ?", session.ID).
			Updates(map[string]any{"status": state, "state_version": version, "updated_at": time.Now().UTC().Add(time.Second)}).Error; err != nil {
			return err
		}
		return tx.Model(&orm.TaskCenterTask{}).Where("id = ?", task.ID).
			Updates(map[string]any{"status": "running", "finished_at": nil}).Error
	})
}

func TestScheduledWorkflowSummaryDoesNotReusePreviousOccurrenceAfterRetry(t *testing.T) {
	db, session, task := scheduledRecoveryFenceFixture(t, "completed")
	if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}); err != nil {
		t.Fatal(err)
	}
	config := DefaultNotificationConfig()
	raw, _ := json.Marshal(config)
	configJSON := string(raw)
	if err := db.Model(&orm.TaskCenterTask{}).Where("id = ?", task.ID).Update("notification_config", configJSON).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		summary := "replacement model summary"
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
			summary = "obsolete model summary"
		}
		w.Header().Set("Content-Type", "application/json")
		content, _ := json.Marshal(map[string]any{"summary": summary})
		_ = json.NewEncoder(w).Encode(map[string]any{"content": string(content)})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	done := make(chan error, 1)
	go func() { _, err := finalizeScheduledOutput(ctx, db, task.ID, task.ConversationID); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("initial finalizer did not reach model request")
	}
	err := acceptScheduledRecoveryRetry(ctx, db, session, task, true)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertScheduledRecoveryRetryUntouched(t, db, task.ID)
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "history").
		Updates(map[string]any{"result": "replacement result", "update_time": time.Now().UTC().Add(time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := finalizeScheduledOutput(ctx, db, task.ID, task.ConversationID); err != nil {
		t.Fatal(err)
	}
	var output orm.TaskRunOutput
	if err := db.Where("task_id = ?", task.ID).First(&output).Error; err != nil {
		t.Fatal(err)
	}
	if output.FinalAnswerText != "replacement result" || output.SummaryText != "replacement model summary" || calls.Load() != 2 {
		t.Fatalf("old model cache polluted replacement: output=%#v model_calls=%d", output, calls.Load())
	}
}

func assertScheduledRecoveryRetryUntouched(t *testing.T, db *gorm.DB, taskID string) {
	t.Helper()
	var task orm.TaskCenterTask
	if err := db.First(&task, "id = ?", taskID).Error; err != nil {
		t.Fatal(err)
	}
	var notices, outputs int64
	if err := db.Model(&orm.TaskNotification{}).Where("task_id = ?", taskID).Count(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.TaskRunOutput{}).Where("task_id = ?", taskID).Count(&outputs).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.FinishedAt != nil || notices != 0 || outputs != 0 {
		t.Fatalf("old workflow recovery ended accepted retry: task=%s finished=%v notices=%d outputs=%d", task.Status, task.FinishedAt, notices, outputs)
	}
}

func scheduledRecoveryQueryBarrier(t *testing.T, db *gorm.DB, ctx context.Context, table string) (<-chan struct{}, func()) {
	t.Helper()
	queried, resume := make(chan struct{}), make(chan struct{})
	var stopped atomic.Bool
	if err := db.Callback().Query().After("gorm:query").Register("test:scheduled-recovery-barrier", func(tx *gorm.DB) {
		if tx.Statement.Table != table || !stopped.CompareAndSwap(false, true) {
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
	t.Cleanup(func() { db.Callback().Query().Remove("test:scheduled-recovery-barrier") })
	return queried, func() { close(resume) }
}

func TestScheduledWorkflowRecoveryRechecksSessionAfterSQLiteRetry(t *testing.T) {
	for _, state := range []string{"failed", "waiting"} {
		t.Run(state, func(t *testing.T) {
			db, session, task := scheduledRecoveryFenceFixture(t, state)
			if db.Dialector.Name() != "sqlite" {
				t.Skip("SQLite WAL snapshot retry")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			queried, resume := scheduledRecoveryQueryBarrier(t, db, ctx, (orm.WorkflowSession{}).TableName())
			done := make(chan error, 1)
			go func() { _, err := ReconcileScheduledNotifications(ctx, db, "", time.Now().UTC()); done <- err }()
			select {
			case <-queried:
			case <-ctx.Done():
				t.Fatal("recovery did not reach session snapshot")
			}
			err := acceptScheduledRecoveryRetry(ctx, db, session, task, false)
			resume()
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			assertScheduledRecoveryRetryUntouched(t, db, task.ID)
		})
	}
}

func TestScheduledWorkflowSuccessCommitFencesRetryDuringOutputCollection(t *testing.T) {
	for _, completeReplacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement_active", true: "replacement_completed"}[completeReplacement], func(t *testing.T) {
			db, session, task := scheduledRecoveryFenceFixture(t, "completed")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			queried, resume := scheduledRecoveryQueryBarrier(t, db, ctx, "conversation_artifacts")
			done := make(chan error, 1)
			go func() { _, err := finalizeScheduledOutput(ctx, db, task.ID, task.ConversationID); done <- err }()
			select {
			case <-queried:
			case <-ctx.Done():
				t.Fatal("finalizer did not reach output collection")
			}
			// Output/model work must not hold the session lock and block retry admission.
			err := acceptScheduledRecoveryRetry(ctx, db, session, task, completeReplacement)
			resume()
			if err != nil {
				t.Fatalf("retry could not proceed during output work: %v", err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			assertScheduledRecoveryRetryUntouched(t, db, task.ID)
		})
	}
}

func TestScheduledWorkflowActiveSessionIgnoresOlderTerminalHistory(t *testing.T) {
	for _, historyState := range []string{"failed", "interrupted", "cancelled", "ask_pending"} {
		t.Run(historyState, func(t *testing.T) {
			db, _, task := scheduledRecoveryFenceFixture(t, "active")
			updates := map[string]any{"run_status": historyState}
			if historyState == "ask_pending" {
				updates["ext"] = []byte(`{"ask_pending":{"ask_id":"old-ask","title":"old question"}}`)
			}
			if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "history").Updates(updates).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := finalizeScheduledOutput(t.Context(), db, task.ID, task.ConversationID); err != nil {
				t.Fatal(err)
			}
			assertScheduledRecoveryRetryUntouched(t, db, task.ID)
		})
	}
}

func TestScheduledWorkflowRecoveryLocksSessionBeforeTaskOnPostgres(t *testing.T) {
	for _, state := range []string{"failed", "waiting", "completed"} {
		t.Run(state, func(t *testing.T) {
			db, session, task := scheduledRecoveryFenceFixture(t, state)
			if db.Dialector.Name() != "postgres" {
				t.Skip("PostgreSQL row locking")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			mutation := db.WithContext(ctx).Begin()
			if mutation.Error != nil {
				t.Fatal(mutation.Error)
			}
			defer mutation.Rollback()
			if err := mutation.Model(&orm.WorkflowSession{}).Where("id = ?", session.ID).
				Updates(map[string]any{"status": "active", "state_version": session.StateVersion + 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := mutation.Model(&orm.TaskCenterTask{}).Where("id = ?", task.ID).Update("status", "running").Error; err != nil {
				t.Fatal(err)
			}
			queried := make(chan struct{}, 1)
			if err := db.Callback().Query().After("gorm:query").Register("test:scheduled-session-lock", func(tx *gorm.DB) {
				if tx.Statement.Table == (orm.WorkflowSession{}).TableName() {
					select {
					case queried <- struct{}{}:
					default:
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Callback().Query().Remove("test:scheduled-session-lock") })
			done := make(chan error, 1)
			go func() { _, err := ReconcileScheduledNotifications(ctx, db, "", time.Now().UTC()); done <- err }()
			readBeforeLock := false
			select {
			case <-queried:
				readBeforeLock = true
			case <-time.After(150 * time.Millisecond):
			}
			if err := mutation.Commit().Error; err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if readBeforeLock {
				t.Error("recovery read a terminal session without waiting for its retry lock")
			}
			assertScheduledRecoveryRetryUntouched(t, db, task.ID)
		})
	}
}
