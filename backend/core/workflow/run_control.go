package workflow

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
)

// Resume and dispatch share the transition transaction. Invalid targets, pending
// reviews and version conflicts must leave the paused session untouched.
func resumeForRerun(ctx context.Context, tx *gorm.DB, session *orm.WorkflowSession, req transitionCommandRequest) error {
	if req.ExpectedStateVersion != session.StateVersion {
		return controlstore.Reject("STATE_VERSION_CONFLICT", "workflow changed since the rerun request")
	}
	if controlstore.Controlled(*session) || req.RetryOrigin != "user" || len(req.Targets) != 1 ||
		(req.Operation != "retry" && req.Operation != "rewind") {
		return controlstore.Reject("INVALID_RERUN", "explicit native rerun requires one previously executed step")
	}
	display, err := displaystate.Read(tx, *session)
	if err != nil {
		return err
	}
	if display.Status != "paused" || session.Status == "pausing" {
		return controlstore.Reject("WORKFLOW_NOT_PAUSED", "only a settled paused workflow can resume for rerun")
	}
	var history orm.ChatHistory
	if req.TriggerHistoryID == "" {
		return controlstore.Reject("INVALID_RERUN", "rerun must belong to the requesting Chat turn")
	}
	if err := tx.Where("id = ? AND conversation_id = ?", req.TriggerHistoryID, session.ConversationID).First(&history).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return controlstore.Reject("INVALID_RERUN", "rerun Chat turn does not belong to this conversation")
		}
		return err
	}
	binding, err := json.Marshal(nativeTurnBinding{HistoryID: history.ID})
	if err != nil {
		return err
	}
	if err := acceptRunControl(ctx, tx, session, "resume", map[string]any{"control_binding_json": string(binding)}, &req.ExpectedStateVersion); err != nil {
		return err
	}
	session.ControlBindingJSON = string(binding)
	return nil
}

// guardRunAdvance is the native execution admission rule for every controller.
// Target selection, recovery and retry budgets are checked by the same graph
// transition below this boundary. Host binding is only a delivery prerequisite.
func guardRunAdvance(tx *gorm.DB, session orm.WorkflowSession) error {
	if session.Status == "pausing" {
		return controlstore.Reject("WORKFLOW_PAUSING", "wait for execution to stop before continuing")
	}
	display, err := displaystate.Read(tx, session)
	if err != nil {
		return err
	}
	approvalRequired := display.ApprovalRequired()
	if controlstore.Controlled(session) {
		// Running siblings take precedence in display state, but pending reviews
		// still block new grants. Existing grants drain through GuardClaim.
		var pendingReviews int64
		if err := tx.Model(&orm.WorkflowReviewCheckpoint{}).
			Where("session_id = ? AND status = ?", session.ID, "pending").Count(&pendingReviews).Error; err != nil {
			return err
		}
		approvalRequired = approvalRequired || pendingReviews > 0
	}
	if approvalRequired {
		return controlstore.Reject("WORKFLOW_APPROVAL_REQUIRED", "approve the completed step from the workflow controls before continuing")
	}
	if display.Status == "waiting" && display.Reason == "inputs_changed" {
		return controlstore.Reject("EDITS_PENDING_CONTINUE", "continue explicitly to regenerate results from the updated inputs")
	}
	if display.Status == "paused" || session.Status == "stopped" {
		return controlstore.Reject("WORKFLOW_PAUSED", "resume the workflow explicitly before starting another attempt")
	}
	if controlstore.Controlled(session) {
		if err := controlstore.GuardClaim(tx, session); err != nil {
			return err
		}
		if controlstore.EditPaused(session) {
			return controlstore.Reject("EDITS_PENDING_CONTINUE", "continue explicitly after saving edits")
		}
	}
	return nil
}
