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
		// The approval was admitted and its checkpoint persisted by the adapter.
		// Re-project the released run, so a stale waiting hand-off cannot mask
		// graph completion after the final review is accepted.
		admitted := *session
		admitted.Status = runstate.Active
		projected, err := projectSession(ctx, tx, &admitted)
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

// Notifications may request reconciliation, but the durable graph decides the
// state. Publish only an actual change; replay is not a new execution event.
func reconcileRunStatus(ctx context.Context, tx *gorm.DB, session *orm.WorkflowSession) error {
	projected, err := projectSession(ctx, tx, session)
	if err != nil {
		return err
	}
	if projectedRunStatus(*session, projected) == session.Status {
		return nil
	}
	return writeSessionProjection(ctx, tx, session, projected)
}

func projectedRunStatus(session orm.WorkflowSession, projected projectionResponse) string {
	return runstate.AfterProjection(session.Status, projected.Projection, runstate.ProjectionFacts{
		Preparing: projected.Display.Status == "running", Paused: projected.Display.Status == "paused",
	})
}
