package workflow

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

// A controlled execution commits its result, review, route and continuation in
// execution.Service.Complete. The SubAgent stream is only a notification of that
// commit; it has no execution handle and must not run the native scheduler or
// write attempt/session state a second time.
func observeControlledTaskCompletion(ctx context.Context, db *gorm.DB, taskID, status, summary string,
	onSSE func(string, map[string]any), pctx *WorkflowChatContext) (bool, error) {
	var step orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("task_id = ?", taskID).First(&step).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	var session orm.WorkflowSession
	if err := db.WithContext(ctx).First(&session, "id = ?", step.SessionID).Error; err != nil {
		return false, err
	}
	if !controlstore.Controlled(session) {
		return false, nil
	}
	if pctx == nil || pctx.SessionID != session.ID || pctx.StepID != step.StepID ||
		step.Validity == "stale" || step.Status != status || session.Status == "stopped" || session.Status == "pausing" {
		return true, nil
	}
	var current orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("session_id = ? AND step_id = ? AND validity = ?", session.ID, step.StepID, "effective").
		Order("attempt DESC, created_at DESC, id DESC").First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, nil
		}
		return true, err
	}
	if current.ID != step.ID {
		return true, nil
	}
	feedback, err := appendWorkflowStepFeedback(ctx, db, pctx, taskID, status, summary)
	if err != nil {
		return true, err
	}
	if onSSE != nil {
		onSSE("workflow_step_feedback", map[string]any{"session_id": session.ID, "step_id": step.StepID,
			"task_id": taskID, "history_id": pctx.TriggerHistoryID, "status": status, "message": feedback})
	}
	return true, nil
}
