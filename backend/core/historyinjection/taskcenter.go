package historyinjection

import (
	"context"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func reconcileInjectedWorkflowTasks(ctx context.Context, db *gorm.DB, manifest Manifest, owner TargetOwner) error {
	if len(manifest.SessionIDs) == 0 {
		return nil
	}

	var sessions []orm.WorkflowSession
	if err := db.WithContext(ctx).
		Where("id IN ? AND conversation_id = ? AND create_user_id = ?", manifest.SessionIDs, manifest.ConversationID, owner.ID).
		Order("created_at, id").Find(&sessions).Error; err != nil {
		return err
	}
	if len(sessions) == 0 {
		return nil
	}

	var linkedSessionIDs []string
	if err := db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("plugin_session_id IN ?", manifest.SessionIDs).Pluck("plugin_session_id", &linkedSessionIDs).Error; err != nil { // workflow-naming: persistence
		return err
	}
	linked := make(map[string]bool, len(linkedSessionIDs))
	for _, sessionID := range linkedSessionIDs {
		linked[sessionID] = true
	}

	// Older history bundles can contain one background task and one Workflow
	// session for the same run without linking them. Preserve that single task
	// instead of adding a duplicate entry to Task Center.
	if len(sessions) == 1 && !linked[sessions[0].ID] {
		var candidates []orm.TaskCenterTask
		if err := db.WithContext(ctx).
			Where("user_id = ? AND conversation_id = ? AND task_type IN ? AND (plugin_session_id IS NULL OR plugin_session_id = '')", // workflow-naming: persistence
				owner.ID, manifest.ConversationID, []string{"background_chat", "scheduled"}).
			Order("created_at, id").Limit(2).Find(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) == 1 {
			result := db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
				Where("id = ? AND (plugin_session_id IS NULL OR plugin_session_id = '')", candidates[0].ID). // workflow-naming: persistence
				Update("plugin_session_id", sessions[0].ID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				linked[sessions[0].ID] = true
			}
		}
	}

	for _, session := range sessions {
		if linked[session.ID] {
			continue
		}
		status := injectedWorkflowTaskStatus(session.Status)
		var finishedAt *time.Time
		if status == "succeeded" || status == "failed" || status == "canceled" {
			finished := session.UpdatedAt
			finishedAt = &finished
		}
		title := manifest.Title
		task := orm.TaskCenterTask{
			ID:                common.GeneratePrefixedID("tc_", 36),
			UserID:            owner.ID,
			ConversationID:    manifest.ConversationID,
			WorkflowSessionID: &session.ID,
			TaskType:          "workflow_run",
			Title:             &title,
			Status:            status,
			TriggerType:       "manual",
			Attempt:           1,
			DefinitionVersion: 1,
			DependencyStatus:  "none",
			ProgressJSON:      orm.RawJSON(`{}`),
			CreatedAt:         session.CreatedAt,
			UpdatedAt:         session.UpdatedAt,
			FinishedAt:        finishedAt,
		}
		if err := db.WithContext(ctx).Create(&task).Error; err != nil {
			return err
		}
	}
	return nil
}

func injectedWorkflowTaskStatus(status string) string {
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
		return "pending"
	}
}
