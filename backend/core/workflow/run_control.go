package workflow

import (
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
)

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
