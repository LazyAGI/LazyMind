package taskcenter

import (
	"context"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// EnsureWorkflowTask joins a newly created session to its running trigger, or
// registers a standalone workflow task. The caller holds the conversation lock
// and runs this inside the session-creation transaction.
func EnsureWorkflowTask(ctx context.Context, db *gorm.DB, session orm.WorkflowSession) error {
	if session.ConversationID == "" {
		return nil
	}
	var count int64
	if err := db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("plugin_session_id = ?", session.ID).Count(&count).Error; err != nil || count > 0 { // workflow-naming: persistence
		return err
	}
	var triggers []orm.TaskCenterTask
	if err := db.WithContext(ctx).
		Where("user_id = ? AND conversation_id = ? AND task_type IN ? AND status = ? AND archived_at IS NULL", session.CreateUserID, session.ConversationID, []string{"background_chat", "scheduled"}, "running").
		Where("(plugin_session_id IS NULL OR plugin_session_id = '') AND created_at <= ? AND finished_at IS NULL", session.CreatedAt). // workflow-naming: persistence
		Order("created_at DESC").Limit(1).Find(&triggers).Error; err != nil {
		return err
	}
	if len(triggers) == 1 {
		result := db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
			Where("id = ? AND status = ? AND archived_at IS NULL AND (plugin_session_id IS NULL OR plugin_session_id = '')", triggers[0].ID, "running"). // workflow-naming: persistence
			Updates(map[string]any{"plugin_session_id": session.ID, "updated_at": session.CreatedAt})                                                    // workflow-naming: persistence
		if result.Error != nil || result.RowsAffected == 1 {
			return result.Error
		}
	}
	title := session.WorkflowID
	var conversation orm.Conversation
	if err := db.WithContext(ctx).Select("display_name").Where("id = ? AND create_user_id = ?", session.ConversationID, session.CreateUserID).
		First(&conversation).Error; err != nil {
		return err
	}
	if conversation.DisplayName != "" {
		title = conversation.DisplayName
	}
	return CreateTask(ctx, db, &orm.TaskCenterTask{
		UserID: session.CreateUserID, ConversationID: session.ConversationID,
		WorkflowSessionID: &session.ID, TaskType: "workflow_run", Title: &title,
		Status: "running", CreatedAt: session.CreatedAt,
	})
}

// HistoricalWorkflowMatches groups unclaimed sessions within each persisted
// task's execution window. Ambiguous matches never replace the task's status.
// Callers select either the task ID or the unique session ID.
func HistoricalWorkflowMatches(db *gorm.DB) *gorm.DB {
	return db.Table("plugin_sessions").
		Joins("JOIN task_center_tasks ON task_center_tasks.conversation_id = plugin_sessions.conversation_id AND task_center_tasks.user_id = plugin_sessions.create_user_id").
		Where("task_center_tasks.archived_at IS NULL AND task_center_tasks.status <> ? AND task_center_tasks.task_type IN ?", "canceled", []string{"background_chat", "scheduled"}).
		Where("task_center_tasks.created_at <> ? AND plugin_sessions.created_at >= task_center_tasks.created_at", time.Time{}).                                                      // workflow-naming: persistence
		Where("task_center_tasks.finished_at IS NULL OR plugin_sessions.created_at <= task_center_tasks.finished_at").                                                               // workflow-naming: persistence
		Where("task_center_tasks.finished_at IS NOT NULL OR task_center_tasks.status NOT IN ? OR plugin_sessions.created_at <= task_center_tasks.updated_at", terminalTaskStatuses). // workflow-naming: persistence
		Where("NOT EXISTS (SELECT 1 FROM task_center_tasks linked WHERE linked.plugin_session_id = plugin_sessions.id)").
		Where(`NOT EXISTS (SELECT 1 FROM task_center_tasks later WHERE later.user_id = task_center_tasks.user_id
			AND later.conversation_id = task_center_tasks.conversation_id AND later.id <> task_center_tasks.id
			AND later.created_at > task_center_tasks.created_at AND later.created_at <= plugin_sessions.created_at)`).
		Group("task_center_tasks.id").Having("COUNT(*) = 1")
}

func workflowForTask(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask) *orm.WorkflowSession {
	query := db.WithContext(ctx).Model(&orm.WorkflowSession{})
	if task.WorkflowSessionID != nil && *task.WorkflowSessionID != "" {
		var session orm.WorkflowSession
		if err := query.Where("id = ?", *task.WorkflowSessionID).First(&session).Error; err == nil {
			return &session
		}
		return nil
	}
	matches := HistoricalWorkflowMatches(db.WithContext(ctx)).
		Where("task_center_tasks.id = ?", task.ID).Select("MIN(plugin_sessions.id)")
	var session orm.WorkflowSession
	result := query.Where("id IN (?)", matches).Find(&session)
	if result.Error != nil || result.RowsAffected == 0 {
		return nil
	}
	return &session
}

func workflowTaskStatus(status string) string {
	switch status {
	case "active":
		return "running"
	case "waiting":
		return "waiting"
	case "completed":
		return "succeeded"
	case "failed":
		return "failed"
	case "stopped", "cancelled", "canceled":
		return "canceled"
	default:
		return ""
	}
}
