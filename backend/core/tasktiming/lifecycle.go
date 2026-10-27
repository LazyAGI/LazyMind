package tasktiming

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/displaystate"
)

// BindWorkflow retains the accepted Chat request's earlier timestamp. Pauses
// and approvals close an interval; resumption opens another without counting
// the intervening wait. Snapshot sums those intervals for the same execution.
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
			// Accepting a final review can finish without another active interval.
			// Preserve its frozen duration, but close the continuation boundary so
			// a later, genuine rerun does not inherit this execution's time.
			if current.FinishedAt != nil && workflowWait(current.Status) && !workflowWait(session.Status) {
				return db.WithContext(ctx).Model(&current).Updates(map[string]any{
					"status": session.Status, "revision": gorm.Expr("revision + 1"),
				}).Error
			}
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
	view, err := displaystate.Read(db.WithContext(ctx), session)
	if err != nil {
		return err
	}
	switch view.Status {
	case "running":
		// Includes preparation, parallel siblings and confirmed-stop cleanup.
		session.Status = "active"
	case "paused":
		session.Status = "stopped"
	case "waiting":
		// Internal scheduler waiting must not be mistaken for an approval.
		session.Status = "waiting_approval"
	case "failed":
		session.Status = "failed"
	}
	return BindWorkflow(ctx, db, session, now)
}
func FinishChat(ctx context.Context, db *gorm.DB, id, status string, now time.Time) error {
	if !Available(db) {
		return nil
	}
	return transaction(ctx, db, func(tx *gorm.DB) error {
		// Serialize reply completion with child starts and terminal transitions.
		if err := tx.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("id=?", id).UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
			return err
		}
		var root orm.TaskExecutionTiming
		err := tx.WithContext(ctx).Where("id = ? AND workflow_session_id = ''", id).First(&root).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return finishChatRound(ctx, tx, root, status, now)
	})
}

func finishChatRound(ctx context.Context, db *gorm.DB, root orm.TaskExecutionTiming, status string, now time.Time) error {
	if root.FinishedAt != nil {
		return nil
	}
	if (status == "completed" || status == "succeeded") && db.Migrator().HasTable(&orm.SubAgentTask{}) {
		var count int64
		if err := db.WithContext(ctx).Model(&orm.SubAgentTask{}).Where("create_user_id=? AND conversation_id=? AND trigger_history_id=? AND agent_type<>? AND status IN ?", root.OwnerUserID, root.ConversationID, root.HistoryID, "workflow_step", []string{"pending", "running"}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			// The reply is complete; pending/running background children own the cutoff.
			return SetStatus(ctx, db, root.ID, "waiting", now)
		}
	}
	return SetStatus(ctx, db, root.ID, status, now)
}

func RecordSubagent(ctx context.Context, db *gorm.DB, task orm.SubAgentTask) error {
	if !Available(db) || task.ExecutionID == "" || (task.StartedAt == nil && task.FinishedAt == nil) {
		return nil
	}
	return transaction(ctx, db, func(tx *gorm.DB) error { return recordSubagent(ctx, tx, task) })
}

func recordSubagent(ctx context.Context, db *gorm.DB, task orm.SubAgentTask) error {
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
	// Even a pending child canceled before it starts must settle its waiting round.
	if err := db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("id=?", root.ID).UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
		return err
	}
	if task.StartedAt != nil {
		if err := RecordAttempt(ctx, db, root.ID, id, *task.StartedAt, task.FinishedAt); err != nil {
			return err
		}
	}
	if task.FinishedAt != nil {
		if err := db.WithContext(ctx).Where("id=?", root.ID).First(&root).Error; err != nil {
			return err
		}
		if root.WorkflowSessionID == "" && root.Status == "waiting" {
			return finishChatRound(ctx, db, root, "completed", *task.FinishedAt)
		}
	}
	return nil
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
func CancelConversation(ctx context.Context, db *gorm.DB, owner, conversationID string, now time.Time, historyID ...string) error {
	if !Available(db) {
		return nil
	}
	query := db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("owner_user_id = ? AND conversation_id = ? AND finished_at IS NULL", owner, conversationID)
	if len(historyID) > 0 && historyID[0] != "" {
		query = query.Where("history_id = ? AND workflow_session_id = ''", historyID[0])
	}
	return query.Updates(map[string]any{"status": "canceled", "finished_at": now, "revision": gorm.Expr("revision + 1")}).Error
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
