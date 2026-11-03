package workflow

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/runstate"
)

// acceptRunControl is the shared write path for an admitted controller turn.
// Authentication, version checks, review decisions and host delivery belong to
// the adapters. Both adapters must use this path when they accept continuation;
// changing the native rule here therefore changes external continuation too.
func acceptRunControl(ctx context.Context, tx *gorm.DB, session *orm.WorkflowSession, kind string, fields map[string]any, expectedVersion *int64) error {
	approvalPending, completed := false, false
	if kind == "approve" {
		projected, err := projectSession(ctx, tx, session)
		if err != nil {
			return err
		}
		approvalPending, completed = projected.Display.ApprovalRequired(), projected.Projection.Completed
	}
	status := runstate.AfterControl(session.Status, kind, approvalPending, completed)
	writeTx := tx
	if expectedVersion != nil {
		writeTx = tx.Where("state_version = ?", *expectedVersion)
	}
	return runstate.Write(writeTx, session, status, fields)
}
