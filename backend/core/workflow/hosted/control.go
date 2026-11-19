package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
)

func (s *Service) claimExecution(ctx context.Context, owner, sessionID, attemptID string, resume bool, claimID string) (Execution, error) {
	var execution Execution
	var claimed bool
	err := controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		claimed = false
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
			var receipt *orm.WorkflowCommand
			var replay bool
			if claimID != "" && !resume {
				receipt, replay, err = claimCommand(tx, owner, *session, row, claimID)
				if err != nil {
					return err
				}
			}
			if resume {
				claim, err = service.ClaimAttemptForHost(ctx, attemptID, executorID(owner), HostName)
			} else if replay {
				claim = attempt.Claim{LeaseToken: row.LeaseToken, LeaseExpiresAt: *row.LeaseExpiresAt}
			} else {
				claim, err = service.ClaimQueuedAttemptForHost(ctx, attemptID, executorID(owner), HostName)
			}
			if err != nil {
				return &ProtocolError{Code: "EXECUTION_NOT_CLAIMABLE", Message: "execution is already claimed or terminal; use resume only to recover an interrupted execution", Cause: err}
			}
			loader := s.Contexts
			if _, ok := loader.(executor.DBContextLoader); ok && controlstore.Controlled(*session) {
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
			if replay {
				execution.AttemptStatus = row.Status
				return nil
			}
			if receipt != nil {
				receipt.ResponseJSON, err = json.Marshal(claim.FencingGeneration)
				if err != nil {
					return err
				}
				if err := tx.Create(receipt).Error; err != nil {
					return err
				}
			}
			claimed = true
		}
		if controlstore.Controlled(*session) {
			return controlstore.ConsumeContinuation(tx, sessionID, attemptID)
		}
		return nil

	})
	if err == nil && claimed {
		workflowcore.NotifyWorkflowRuntimeUpdated(ctx, s.DB, sessionID, attemptID, "running")
	}
	return execution, err
}

// Store the generation alongside the claim, so a retry can recover the same
// live handle without minting a second lease or retaining a revoked token.
func claimCommand(tx *gorm.DB, owner string, session orm.WorkflowSession, row orm.WorkflowSessionStep, claimID string) (*orm.WorkflowCommand, bool, error) {
	digest := controlstore.Hash([]byte(row.ID + ":" + claimID))
	receipt := orm.WorkflowCommand{CommandID: "workflow-claim:" + digest, OwnerUserID: owner,
		SessionID: session.ID, ContractVersion: "workflow.claim.v1", RequestHash: digest, HTTPStatus: 200, CreatedAt: time.Now().UTC()}
	var saved orm.WorkflowCommand
	err := tx.First(&saved, "command_id = ?", receipt.CommandID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &receipt, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if saved.OwnerUserID != owner || saved.SessionID != session.ID || saved.ContractVersion != receipt.ContractVersion || saved.RequestHash != digest {
		return nil, false, controlstore.Reject("COMMAND_CONFLICT", "claim command was already used for another operation")
	}
	var generation int64
	if err := json.Unmarshal(saved.ResponseJSON, &generation); err != nil {
		return nil, false, err
	}
	if generation != row.FencingGeneration || row.LeaseOwner != executorID(owner) {
		return nil, false, controlstore.Reject("EXECUTION_FENCED", "the execution handle from this claim has been replaced")
	}
	if err := controlstore.ValidateExecution(tx, session, row.ID, row.LeaseToken); err != nil {
		return nil, false, err
	}
	return nil, true, nil
}

func (s *Service) Heartbeat(ctx context.Context, owner, sessionID, attemptID, handle string) (time.Time, error) {
	if err := s.Store.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return time.Time{}, err
	}
	var expires time.Time
	err := controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		var row orm.WorkflowSessionStep
		err := tx.Select("executor_host", "lease_owner").First(&row, "id = ? AND session_id = ?", attemptID, sessionID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return controlstore.Reject("EXECUTION_NOT_FOUND", "execution was not found")
		}
		if err != nil {
			return err
		}
		if row.ExecutorHost == "lazymind" || row.LeaseOwner != executorID(owner) {
			return controlstore.Reject("EXECUTOR_MISMATCH", "the hosted controller does not own this execution")
		}
		if err := controlstore.ValidateExecution(tx, *session, attemptID, handle); err != nil {
			return err
		}
		expires, err = s.Attempts.WithDB(tx).Heartbeat(ctx, attemptID, handle)
		if errors.Is(err, attempt.ErrLeaseLost) {
			return controlstore.Reject("EXECUTION_FENCED", "the execution no longer owns this attempt; resume before submitting")
		}
		return err
	})
	return expires, err
}
