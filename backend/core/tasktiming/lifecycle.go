package tasktiming

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// BindWorkflow retains the accepted chat request's earlier timestamp. A later
// terminal-to-active transition creates a fresh round instead of resetting it.
func BindWorkflow(ctx context.Context, db *gorm.DB, session orm.WorkflowSession, now time.Time) error {
	if !Available(db) {
		return nil
	}
	var current orm.TaskExecutionTiming
	err := db.WithContext(ctx).Where("workflow_session_id = ?", session.ID).Order("requested_at DESC, id DESC").First(&current).Error
	if err == nil && current.FinishedAt == nil {
		return SetStatus(ctx, db, current.ID, session.Status, now)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if terminal(session.Status) {
		if err == nil {
			return SetStatus(ctx, db, current.ID, session.Status, now)
		}
		return nil
	}
	var candidate orm.TaskExecutionTiming
	q := db.WithContext(ctx).Where("conversation_id = ? AND owner_user_id = ? AND workflow_session_id = '' AND finished_at IS NULL", session.ConversationID, session.CreateUserID)
	if err := q.Order("requested_at DESC, id DESC").First(&candidate).Error; err == nil {
		return db.WithContext(ctx).Model(&candidate).Updates(map[string]any{"run_id": session.ID, "workflow_session_id": session.ID, "status": session.Status, "revision": gorm.Expr("revision + 1")}).Error
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	id := uuid.NewString()
	if err := Begin(ctx, db, id, session.ID, session.ConversationID, session.CreateUserID, now); err != nil {
		return err
	}
	return db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("id = ?", id).Updates(map[string]any{"history_id": session.TriggerHistoryID, "workflow_session_id": session.ID, "status": session.Status}).Error
}
func SyncWorkflow(ctx context.Context, db *gorm.DB, sessionID string, now time.Time) error {
	if !Available(db) {
		return nil
	}
	var session orm.WorkflowSession
	if err := db.WithContext(ctx).Where("id = ?", sessionID).First(&session).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var count int64
	if err := db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("workflow_session_id = ?", sessionID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return nil
	} // Do not manufacture a start time for legacy runs.
	return BindWorkflow(ctx, db, session, now)
}
func FinishChat(ctx context.Context, db *gorm.DB, id, status string, now time.Time) error {
	if !Available(db) {
		return nil
	}
	var root orm.TaskExecutionTiming
	err := db.WithContext(ctx).Where("id = ? AND workflow_session_id = ''", id).First(&root).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return SetStatus(ctx, db, id, status, now)
}
func RecordSubagent(ctx context.Context, db *gorm.DB, task orm.SubAgentTask) error {
	if !Available(db) || task.ExecutionID == "" || task.StartedAt == nil {
		return nil
	}
	// A Workflow adapter and its hosted Attempt identify one execution. Only the
	// adapter records its duration; the hosted event path skips such rows.
	var sessionID string
	if task.AgentType == "workflow_step" && db.Migrator().HasTable(&orm.WorkflowSessionStep{}) {
		if err := db.WithContext(ctx).Model(&orm.WorkflowSessionStep{}).Where("task_id = ?", task.ID).Limit(1).Pluck("session_id", &sessionID).Error; err != nil {
			return err
		}
	}
	q := db.WithContext(ctx).Where("conversation_id = ? AND owner_user_id = ?", task.ConversationID, task.CreateUserID)
	if sessionID != "" {
		q = q.Where("workflow_session_id = ?", sessionID)
	} else {
		q = q.Where("history_id = ?", task.TriggerHistoryID)
	}
	var root orm.TaskExecutionTiming
	err := q.Order("requested_at DESC, id DESC").First(&root).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && sessionID == "" {
		// Helper agents spawned by a later confirmation turn still belong to the
		// active Workflow round rather than that turn's temporary chat run.
		err = db.WithContext(ctx).Where("conversation_id = ? AND owner_user_id = ? AND workflow_session_id <> '' AND finished_at IS NULL", task.ConversationID, task.CreateUserID).Order("requested_at DESC, id DESC").First(&root).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Preserve the original round association when a late old task finishes.
	id := "task:" + task.ID + ":" + task.ExecutionID
	var previous orm.TaskAttemptTiming
	if err := db.WithContext(ctx).Where("id = ?", id).First(&previous).Error; err == nil {
		root.ID = previous.ExecutionID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return RecordAttempt(ctx, db, root.ID, id, *task.StartedAt, task.FinishedAt)
}
func SyncSubagent(ctx context.Context, db *gorm.DB, taskID string) error {
	if !Available(db) {
		return nil
	}
	var task orm.SubAgentTask
	if err := db.WithContext(ctx).Where("id = ?", taskID).First(&task).Error; err != nil {
		return err
	}
	return RecordSubagent(ctx, db, task)
}

// RecordWorkflowEvent runs in the event writer's transaction. Never derive time
// from mutable updated_at. Queued/claimed events are not execution starts.
func RecordWorkflowEvent(ctx context.Context, db *gorm.DB, event orm.WorkflowEvent) error {
	if !Available(db) {
		return nil
	}
	if err := SyncWorkflow(ctx, db, event.SessionID, event.CreatedAt); err != nil {
		return err
	}
	if event.EventType != "attempt.patch" && event.EventType != "attempt.progress" {
		return nil
	}
	var row orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("id = ? AND session_id = ?", event.EntityID, event.SessionID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if row.TaskID != "" && db.Migrator().HasTable(&orm.SubAgentTask{}) {
		var count int64
		if err := db.WithContext(ctx).Model(&orm.SubAgentTask{}).Where("id = ?", row.TaskID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return SyncSubagent(ctx, db, row.TaskID)
		}
	}
	var root orm.TaskExecutionTiming
	if err := db.WithContext(ctx).Where("workflow_session_id = ?", row.SessionID).Order("requested_at DESC, id DESC").First(&root).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	id := "attempt:" + row.ID
	var old orm.TaskAttemptTiming
	err := db.WithContext(ctx).Where("id = ?", id).First(&old).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		root.ID = old.ExecutionID
	}
	if row.Status == "running" {
		return RecordAttempt(ctx, db, root.ID, id, event.CreatedAt, nil)
	}
	if terminal(row.Status) && err == nil {
		return RecordAttempt(ctx, db, root.ID, id, old.StartedAt, &event.CreatedAt)
	}
	return nil
}
func CancelConversation(ctx context.Context, db *gorm.DB, owner, conversationID string, now time.Time) error {
	if !Available(db) {
		return nil
	}
	return db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("owner_user_id = ? AND conversation_id = ? AND finished_at IS NULL", owner, conversationID).Updates(map[string]any{"status": "canceled", "finished_at": now, "revision": gorm.Expr("revision + 1")}).Error
}

// Continuations of an active Workflow keep its root clock, even when the user
// sends another history turn to answer a confirmation request.
func BeginChat(ctx context.Context, db *gorm.DB, id, history, conversation, owner string, now time.Time) error {
	if !Available(db) {
		return nil
	}
	var count int64
	if err := db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("owner_user_id = ? AND conversation_id = ? AND workflow_session_id <> '' AND finished_at IS NULL", owner, conversation).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return Begin(ctx, db, id, history, conversation, owner, now)
}

// PersistWorkflowEvent commits timing facts and their notification together.
func PersistWorkflowEvent(tx *gorm.DB, event *orm.WorkflowEvent) error {
	if err := RecordWorkflowEvent(tx.Statement.Context, tx, *event); err != nil {
		return err
	}
	return tx.Create(event).Error
}

// PurgeConversations joins the existing permanent deletion transaction; soft
// deletion keeps timing facts available for recovery.
func PurgeConversations(ctx context.Context, db *gorm.DB, owner string, ids []string) error {
	if !Available(db) {
		return nil
	}
	roots := db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Select("id").Where("owner_user_id = ? AND conversation_id IN ?", owner, ids)
	if err := db.WithContext(ctx).Where("execution_id IN (?)", roots).Delete(&orm.TaskAttemptTiming{}).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Where("owner_user_id = ? AND conversation_id IN ?", owner, ids).Delete(&orm.TaskExecutionTiming{}).Error
}
