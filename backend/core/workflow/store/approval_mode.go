package store

import (
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// ApplySavedApprovalMode freezes preferences even when none exist. An existing
// run must never read defaults changed for future runs in another conversation.
func ApplySavedApprovalMode(db *gorm.DB, session *orm.WorkflowSession) error {
	var preferences []orm.WorkflowApprovalPreference
	if err := db.Where("user_id = ? AND workflow_id = ?", session.CreateUserID, session.WorkflowID).Find(&preferences).Error; err != nil {
		return err
	}
	session.ApprovalPreferences = make(map[string]bool, len(preferences))
	// Auto selected at launch must opt out of package reviews even when this
	// workflow has no saved preference. Dynamic keeps package checkpoints.
	if session.WorkflowMode == "auto" {
		session.ApprovalMode = "auto"
	}
	for _, preference := range preferences {
		session.ApprovalPreferences[preference.StepID] = preference.ApprovalRequired
		if preference.StepID == "*" {
			session.ApprovalMode = "auto"
			if preference.ApprovalRequired {
				session.ApprovalMode = "dynamic"
			}
			session.WorkflowMode = session.ApprovalMode
		}
	}
	return nil
}
