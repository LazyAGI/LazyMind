package taskcenter

import (
	"context"

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

func workflowForTask(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask) *orm.WorkflowSession {
	query := db.WithContext(ctx).Model(&orm.WorkflowSession{})
	if task.WorkflowSessionID != nil && *task.WorkflowSessionID != "" {
		var session orm.WorkflowSession
		if err := query.Where("id = ?", *task.WorkflowSessionID).First(&session).Error; err == nil {
			return &session
		}
		return nil
	}
	if task.ArchivedAt != nil || task.Status == "canceled" || task.CreatedAt.IsZero() ||
		(task.TaskType != "background_chat" && task.TaskType != "scheduled") {
		return nil
	}
	// Older facade sessions were never registered with TaskCenter. Match only an
	// unclaimed session created during this execution, not a later chat turn.
	query = query.Where("create_user_id = ? AND conversation_id = ? AND created_at >= ?", task.UserID, task.ConversationID, task.CreatedAt)
	if task.FinishedAt != nil {
		query = query.Where("created_at <= ?", *task.FinishedAt)
	} else if isTerminal(task.Status) {
		query = query.Where("created_at <= ?", task.UpdatedAt)
	}
	query = query.Where(`NOT EXISTS (SELECT 1 FROM task_center_tasks linked WHERE linked.plugin_session_id = plugin_sessions.id)`).
		Where(`NOT EXISTS (SELECT 1 FROM task_center_tasks later WHERE later.user_id = ? AND later.conversation_id = ? AND later.id <> ? AND later.created_at > ? AND later.created_at <= plugin_sessions.created_at)`,
			task.UserID, task.ConversationID, task.ID, task.CreatedAt)
	var sessions []orm.WorkflowSession
	if err := query.Limit(2).Find(&sessions).Error; err != nil || len(sessions) != 1 {
		return nil
	}
	return &sessions[0]
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
