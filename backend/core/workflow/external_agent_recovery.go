package workflow

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
)

// collectRecoveredExternalTask is a narrow result-only exception to failed Task
// finality. Runtime alone decides Session completion; no Attempt is advanced here.
func collectRecoveredExternalTask(ctx context.Context, db *gorm.DB, taskID string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task orm.ExternalAgentWorkflowTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id=?", taskID).Error; err != nil {
			return err
		}
		if task.Status != externalTaskStatusFailed || task.Stage != "execute" || task.ErrorCode != "WORKFLOW_EXECUTION_FAILED" ||
			task.OwnerUserID == "" || task.SessionID == "" || task.ConversationID == "" || task.WorkflowRevisionID == "" || task.WorkflowRef == "" {
			return nil
		}
		var session orm.WorkflowSession
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id=? AND create_user_id=? AND conversation_id=? AND plugin_revision_id=? AND plugin_ref=? AND status=? AND dismissed=? AND origin_host=? AND controller_host=?", // workflow-naming: persistence — legacy physical columns.
			task.SessionID, task.OwnerUserID, task.ConversationID, task.WorkflowRevisionID, task.WorkflowRef, "completed", false, "external-agent", "lazymind").First(&session).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		projection, err := projectSession(ctx, tx, &session)
		if err != nil {
			return err
		}
		if !projection.Projection.Completed {
			return nil
		}
		updates, err := externalTaskResultUpdates(ctx, tx, task)
		if err != nil {
			return err
		}
		request := decodeJSONMap(task.RequestJSON)
		request["progress"] = map[string]any{
			"total_steps": len(projection.Projection.Nodes), "completed_steps": len(projection.Projection.Past),
			"current_steps": projection.Projection.Current, "blocked_steps": projection.Projection.Blocked,
			"skipped_steps": len(projection.Projection.Pruned) + len(projection.Projection.Bypassed),
		}
		updates["request_json"] = mustJSON(request)
		// Keep the general terminal write guard unchanged. These writes are atomic
		// with the locked eligibility checks and the existing chat result projection.
		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.First(&task, "id=?", task.ID).Error; err != nil {
			return err
		}
		return persistExternalWorkflowChat(tx, task)
	})
}
