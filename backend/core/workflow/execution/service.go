package execution

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	workflowstore "lazymind/core/workflow/store"
	"strings"
	"time"
)

// Service finalizes opted-in external runs, including steps delegated to LazyMind.
type Service struct {
	DB       *gorm.DB
	Store    *workflowstore.Repository
	Attempts *attempt.Service
	Contexts executor.ContextLoader
}

type CompletionResult struct {
	Receipt         *CompletionReceipt     `json:"receipt,omitempty"`
	Control         *controlstore.Snapshot `json:"control,omitempty"`
	ExecutionID     string                 `json:"execution_id"`
	AttemptStatus   string                 `json:"attempt_status"`
	AlreadyTerminal bool                   `json:"already_terminal,omitempty"`
}

type CompletionReceipt struct {
	CommandID   string `json:"command_id"`
	ExecutionID string `json:"execution_id"`
}

func normalizeOutcome(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "succeeded":
		return "succeeded", nil
	case "failure", "failed":
		return "failed", nil
	case "cancel", "cancelled", "canceled":
		return "cancelled", nil
	default:
		return "", controlstore.Reject("INVALID_OUTCOME", "outcome must be succeeded, failed or cancelled")
	}
}

// Complete settles already-published outputs, terminal state, review and receipt together.
func (s *Service) Complete(ctx context.Context, owner, sessionID, attemptID string, input executor.Completion) (CompletionResult, error) {
	if input.ExecutionHandle == "" {
		return CompletionResult{}, controlstore.Reject("EXECUTION_FENCED", "execution_handle is required")
	}
	if err := s.Store.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return CompletionResult{}, err
	}
	status, err := normalizeOutcome(input.Outcome)
	if err != nil {
		return CompletionResult{}, err
	}
	canonical := input
	canonical.ExecutionHandle = ""
	canonical.Outcome = status
	body, err := json.Marshal(canonical)
	if err != nil {
		return CompletionResult{}, err
	}
	digest := controlstore.Hash(body)
	commandID := "complete:" + attemptID
	var result CompletionResult
	err = controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if !controlstore.Controlled(*session) {
			return controlstore.Reject("CONTROL_PROTOCOL_REQUIRED", "use the existing completion protocol for this session")
		}
		var row orm.WorkflowSessionStep
		if err := tx.Where("id = ? AND session_id = ?", attemptID, sessionID).First(&row).Error; err != nil {
			return err
		}
		result = CompletionResult{ExecutionID: attemptID, AttemptStatus: status,
			Receipt: &CompletionReceipt{CommandID: commandID, ExecutionID: attemptID}}
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
		if status == "succeeded" {
			if err := executor.ValidateRequiredOutputs(ctx, tx, contract); err != nil {
				return controlstore.Reject("REQUIRED_OUTPUT_MISSING", err.Error())
			}
		}
		failure := ""
		if status == "failed" {
			failure = strings.TrimSpace(input.Summary)
		}
		terminal, err := json.Marshal(executor.Result{Error: failure, PostStepCheckpoint: input.PostStepCheckpoint, Summary: strings.TrimSpace(input.Summary), ExecutorRef: strings.TrimSpace(input.ExecutorRef), Control: input.Control})
		if err != nil {
			return err
		}
		code := strings.TrimSpace(input.ErrorCode)
		if status == "failed" && code == "" {
			code = "EXECUTION_FAILED"
		}
		if err := s.Attempts.WithControlTransaction(tx).Terminal(ctx, attemptID, input.ExecutionHandle, status, code, terminal); err != nil {
			return err
		}
		if err := tx.Model(&row).Update("submission_hash", digest).Error; err != nil {
			return err
		}
		if status == "succeeded" && controlstore.Controlled(*session) {
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
		if controlstore.Controlled(*session) {
			if err := controlstore.RefreshReviews(tx, session); err != nil {
				return err
			}
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
		if result.Control != nil && row.ExecutorHost == "lazymind" && result.Control.Binding.Bound && result.Control.ActiveExecutions == 0 &&
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
