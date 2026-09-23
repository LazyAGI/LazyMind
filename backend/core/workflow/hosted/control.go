package hosted

import (
	"context"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
)

func (s *Service) beginControlled(ctx context.Context, owner, sessionID, attemptID string, resume bool) (Execution, error) {
	var execution Execution
	err := controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		var row orm.WorkflowSessionStep
		if err := tx.Where("id = ? AND session_id = ?", attemptID, sessionID).First(&row).Error; err != nil {
			return err
		}
		if err := controlstore.GuardClaim(tx, *session); err != nil {
			return err
		}
		if row.ExecutorHost == "lazymind" {
			execution = Execution{ExecutorHost: row.ExecutorHost, ExecutionID: row.ID, AttemptStatus: row.Status,
				ReviewAfterComplete: row.ReviewRequired}
		} else {
			service := s.Attempts.WithDB(tx)
			var claim attempt.Claim
			var err error
			if resume {
				claim, err = service.ClaimAttemptForHost(ctx, attemptID, executorID(owner), HostName)
			} else {
				claim, err = service.ClaimQueuedAttemptForHost(ctx, attemptID, executorID(owner), HostName)
			}
			if err != nil {
				return &ProtocolError{Code: "EXECUTION_NOT_CLAIMABLE", Message: "execution is already claimed or terminal; use resume only to recover an interrupted execution", Cause: err}
			}
			loader := s.Contexts
			if _, ok := loader.(executor.DBContextLoader); ok {
				if err := executor.FreezeControlledInputs(ctx, tx, attemptID); err != nil {
					return err
				}
				loader = executor.DBContextLoader{DB: tx}
			}
			contract, err := loader.LoadAttemptContext(ctx, attemptID)
			if err != nil {
				return err
			}
			contract.Metadata = nil
			execution = Execution{ExecutorHost: HostName, AttemptStatus: "claimed", ReviewAfterComplete: row.ReviewRequired, ExecutionID: attemptID, ExecutionHandle: claim.LeaseToken, LeaseExpires: claim.LeaseExpiresAt, StepContract: contract}
		}
		return controlstore.ConsumeContinuation(tx, sessionID, attemptID)

	})
	if err == nil && execution.ExecutionHandle != "" {
		workflowcore.NotifyWorkflowRuntimeUpdated(ctx, s.DB, sessionID, attemptID, "running")
	}
	return execution, err
}
