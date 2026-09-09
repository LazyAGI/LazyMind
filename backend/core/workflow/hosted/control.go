package hosted

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlpolicy"
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
				ReviewAfterSubmit: row.ReviewRequired}
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
			execution = Execution{ExecutorHost: HostName, AttemptStatus: "claimed", ReviewAfterSubmit: row.ReviewRequired, ExecutionID: attemptID, ExecutionHandle: claim.LeaseToken, LeaseExpires: claim.LeaseExpiresAt, StepContract: contract}
		}
		return controlstore.ConsumeContinuation(tx, sessionID, attemptID)

	})
	if err == nil && execution.ExecutionHandle != "" {
		workflowcore.NotifyWorkflowRuntimeUpdated(ctx, s.DB, sessionID, attemptID, "running")
	}
	return execution, err
}

// submitControlled owns the artifact, terminal, review, projection and receipt transaction.
func (s *Service) submitControlled(ctx context.Context, owner, sessionID, attemptID string, input Submission) (SubmissionResult, error) {
	if err := s.Store.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return SubmissionResult{}, err
	}
	status, err := normalizeOutcome(input.Outcome)
	if err != nil {
		return SubmissionResult{}, err
	}
	canonical := input
	canonical.ExecutionHandle = ""
	canonical.Outcome = status
	body, err := json.Marshal(canonical)
	if err != nil {
		return SubmissionResult{}, err
	}
	digest := controlstore.Hash(body)
	commandID := "submit:" + attemptID
	var result SubmissionResult
	staged, cleanup, err := executor.StageArtifacts(sessionID, input.Artifacts)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil || result.AlreadyTerminal {
			cleanup()
		}
	}()
	err = controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		var row orm.WorkflowSessionStep
		if err := tx.Where("id = ? AND session_id = ?", attemptID, sessionID).First(&row).Error; err != nil {
			return err
		}
		result = SubmissionResult{ExecutionID: attemptID, AttemptStatus: status,
			Receipt: &SubmissionReceipt{CommandID: commandID, ExecutionID: attemptID}}
		if row.SubmissionHash != "" {
			if row.SubmissionHash != digest || row.Status != status {
				return controlstore.Reject("COMMAND_CONFLICT", "execution was already settled with different content")
			}
			result.AlreadyTerminal = true
			var err error
			result.Control, err = controlstore.Read(tx, *session)
			return err
		}
		if err := controlstore.ValidateExecution(tx, *session, attemptID, input.ExecutionHandle); err != nil {
			return err
		}
		loader := s.Contexts
		if _, ok := loader.(executor.DBContextLoader); ok {
			loader = executor.DBContextLoader{DB: tx}
		}
		contract, err := loader.LoadAttemptContext(ctx, attemptID)
		if err != nil {
			return err
		}
		contract.ExecutionHandle = input.ExecutionHandle
		artifacts, err := validateArtifacts(contract, staged)
		if err != nil {
			return err
		}
		sink := s.Artifacts
		if _, ok := sink.(executor.DBArtifactSink); ok {
			sink = executor.DBArtifactSink{DB: tx}
		}
		for _, artifact := range artifacts {
			if err := sink.Save(ctx, contract, artifact); err != nil {
				return err
			}
		}
		if status == "succeeded" {
			if err := executor.ValidateRequiredOutputs(ctx, tx, contract); err != nil {
				return &ProtocolError{Code: "REQUIRED_OUTPUT_MISSING", Message: err.Error()}
			}
		}
		terminal, err := json.Marshal(executor.Result{Summary: strings.TrimSpace(input.Summary), ExecutorRef: strings.TrimSpace(input.ExecutorRef), Control: input.Control})
		if err != nil {
			return err
		}
		code := strings.TrimSpace(input.ErrorCode)
		if status == "failed" && code == "" {
			code = "EXTERNAL_AGENT_FAILED"
		}
		if err := s.Attempts.WithControlTransaction(tx).Terminal(ctx, attemptID, input.ExecutionHandle, status, code, terminal); err != nil {
			return err
		}
		if err := tx.Model(&row).Update("submission_hash", digest).Error; err != nil {
			return err
		}
		if status == "succeeded" {
			slots := contract.DeclaredOutputs
			if len(slots) == 0 {
				slots = contract.RequiredOutputs
			}
			if err := controlstore.CreateReview(tx, *session, row, slots); err != nil {
				return err
			}
		}
		if err := workflowcore.FinalizeHostAttempt(ctx, tx, sessionID, row.StepID, attemptID, status); err != nil {
			return err
		}
		if err := controlstore.RefreshReviews(tx, session); err != nil {
			return err
		}
		if err := controlstore.BumpEvent(tx, session, "execution.settled", attemptID, commandID, result.Receipt); err != nil {
			return err
		}
		receipt, err := json.Marshal(result.Receipt)
		if err != nil {
			return err
		}
		if err := tx.Create(&orm.WorkflowCommand{CommandID: commandID, OwnerUserID: owner, SessionID: sessionID,
			ContractVersion: controlpolicy.Protocol, RequestHash: digest, HTTPStatus: 200, ResponseJSON: receipt, CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		result.Control, err = controlstore.Read(tx, *session)
		if err != nil {
			return err
		}
		if row.ExecutorHost == "lazymind" && result.Control.Binding.Bound && result.Control.ActiveExecutions == 0 &&
			(result.Control.Continuation == "continue" || result.Control.Continuation == "completed" || result.Control.Continuation == "failed") {
			if _, err := controlstore.EnqueueHostAction(tx, *session, commandID, "continue", attemptID); err != nil {
				return err
			}
			result.Control, err = controlstore.Read(tx, *session)
		}
		return err
	})
	if err == nil && !result.AlreadyTerminal {
		workflowcore.NotifyWorkflowRuntimeUpdated(ctx, s.DB, sessionID, attemptID, status)
	}
	return result, err
}
