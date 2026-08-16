package hosted

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/executor"
	workflowstore "lazymind/core/workflow/store"
)

const HostName = "external-agent"

const maxArtifactsPerSubmission = 32

type Service struct {
	DB        *gorm.DB
	Store     *workflowstore.Repository
	Attempts  *attempt.Service
	Contexts  executor.ContextLoader
	Artifacts executor.ArtifactSink
}

type Execution struct {
	ExecutionID  string                  `json:"execution_id"`
	LeaseExpires time.Time               `json:"lease_expires_at"`
	StepContract executor.AttemptContext `json:"step_contract"`
}

type Submission struct {
	Outcome     string              `json:"outcome"`
	Summary     string              `json:"summary,omitempty"`
	ErrorCode   string              `json:"error_code,omitempty"`
	ExecutorRef string              `json:"executor_ref,omitempty"`
	Artifacts   []executor.Artifact `json:"artifacts,omitempty"`
}

type SubmissionResult struct {
	ExecutionID     string `json:"execution_id"`
	AttemptStatus   string `json:"attempt_status"`
	AlreadyTerminal bool   `json:"already_terminal,omitempty"`
}

type ProtocolError struct {
	Code    string
	Message string
	Cause   error
}

func (e *ProtocolError) Error() string { return e.Message }
func (e *ProtocolError) Unwrap() error { return e.Cause }

func executorID(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return "hosted:" + hex.EncodeToString(sum[:16])
}

func (s *Service) Begin(ctx context.Context, owner, sessionID, attemptID string) (Execution, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(attemptID) == "" {
		return Execution{}, &ProtocolError{Code: "INVALID_EXECUTION", Message: "owner, session_id and execution_id are required"}
	}
	if err := s.Store.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return Execution{}, err
	}
	row, err := s.Attempts.Attempt(ctx, attemptID)
	if err != nil || row.SessionID != sessionID {
		return Execution{}, &ProtocolError{Code: "EXECUTION_NOT_FOUND", Message: "hosted execution was not found", Cause: err}
	}
	claim, err := s.Attempts.ClaimAttemptForHost(ctx, attemptID, executorID(owner), HostName)
	if err != nil {
		return Execution{}, &ProtocolError{Code: "EXECUTION_NOT_CLAIMABLE", Message: "hosted execution cannot be claimed or resumed", Cause: err}
	}
	contract, err := s.Contexts.LoadAttemptContext(ctx, attemptID)
	if err != nil {
		return Execution{}, &ProtocolError{Code: "STEP_CONTRACT_UNAVAILABLE", Message: "step execution contract is unavailable", Cause: err}
	}
	// Metadata is useful to trusted workers but contains Runtime bookkeeping
	// (owner, conversation and task identifiers). It is not part of the public
	// external-Agent contract.
	contract.Metadata = nil
	workflowcore.NotifyWorkflowRuntimeUpdated(ctx, s.DB, sessionID, attemptID, "running")
	return Execution{ExecutionID: attemptID, LeaseExpires: claim.LeaseExpiresAt, StepContract: contract}, nil
}

func (s *Service) Resume(ctx context.Context, owner, sessionID, attemptID string) (Execution, error) {
	return s.Begin(ctx, owner, sessionID, attemptID)
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
		return "", &ProtocolError{Code: "INVALID_OUTCOME", Message: "outcome must be succeeded, failed or cancelled"}
	}
}

func validateArtifacts(contract executor.AttemptContext, values []executor.Artifact) ([]executor.Artifact, error) {
	if len(values) > maxArtifactsPerSubmission {
		return nil, &ProtocolError{Code: "TOO_MANY_ARTIFACTS", Message: "one submission may contain at most 32 artifacts"}
	}
	outputs := contract.DeclaredOutputs
	if len(outputs) == 0 {
		outputs = contract.RequiredOutputs
	}
	declared := make(map[string]bool, len(outputs))
	for _, slot := range outputs {
		declared[slot] = true
	}
	seen := make(map[string]bool, len(values))
	normalized := make([]executor.Artifact, len(values))
	copy(normalized, values)
	for i := range normalized {
		artifact := &normalized[i]
		artifact.Slot = strings.TrimSpace(artifact.Slot)
		if !declared[artifact.Slot] {
			return nil, &ProtocolError{Code: "OUTPUT_SLOT_UNDECLARED", Message: "artifact slot is not declared by the step: " + artifact.Slot}
		}
		if len(artifact.Value) == 0 || string(artifact.Value) == "null" {
			return nil, &ProtocolError{Code: "INVALID_ARTIFACT", Message: "artifact value is required: " + artifact.Slot}
		}
		if artifact.Seq < 1 {
			artifact.Seq = 1
		}
		if artifact.ContentType == "" {
			artifact.ContentType = "application/json"
		}
		key := fmt.Sprintf("%s#%d", artifact.Slot, artifact.Seq)
		if seen[key] {
			return nil, &ProtocolError{Code: "DUPLICATE_ARTIFACT", Message: "artifact slot and sequence must be unique: " + key}
		}
		seen[key] = true
	}
	return normalized, nil
}

func (s *Service) Submit(ctx context.Context, owner, sessionID, attemptID string, submission Submission) (SubmissionResult, error) {
	if err := s.Store.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return SubmissionResult{}, err
	}
	status, err := normalizeOutcome(submission.Outcome)
	if err != nil {
		return SubmissionResult{}, err
	}
	row, err := s.Attempts.Attempt(ctx, attemptID)
	if err != nil || row.SessionID != sessionID {
		return SubmissionResult{}, &ProtocolError{Code: "EXECUTION_NOT_FOUND", Message: "hosted execution was not found", Cause: err}
	}
	if row.Status == "succeeded" || row.Status == "failed" || row.Status == "cancelled" || row.Status == "interrupted" {
		if row.Status != status {
			return SubmissionResult{}, &ProtocolError{Code: attempt.CodeAlreadyTerminal, Message: "hosted execution already has another terminal outcome"}
		}
		if err := workflowcore.FinalizeHostAttempt(ctx, s.DB, sessionID, row.StepID, attemptID, status); err != nil {
			return SubmissionResult{}, &ProtocolError{Code: "WORKFLOW_PROJECTION_FAILED", Message: "terminal execution could not update Workflow projection", Cause: err}
		}
		workflowcore.NotifyWorkflowRuntimeUpdated(ctx, s.DB, sessionID, attemptID, status)
		return SubmissionResult{ExecutionID: attemptID, AttemptStatus: status, AlreadyTerminal: true}, nil
	}
	if row.LeaseOwner != executorID(owner) {
		return SubmissionResult{}, &ProtocolError{Code: attempt.CodeLeaseLost, Message: "hosted execution must be begun or resumed before submission"}
	}
	if err := s.Attempts.ValidateLease(ctx, attemptID, row.LeaseToken); err != nil {
		return SubmissionResult{}, &ProtocolError{Code: attempt.CodeLeaseLost, Message: "hosted execution lease expired; resume it before submission", Cause: err}
	}
	contract, err := s.Contexts.LoadAttemptContext(ctx, attemptID)
	if err != nil {
		return SubmissionResult{}, &ProtocolError{Code: "STEP_CONTRACT_UNAVAILABLE", Message: "step execution contract is unavailable", Cause: err}
	}
	artifacts, err := validateArtifacts(contract, submission.Artifacts)
	if err != nil {
		return SubmissionResult{}, err
	}
	for _, artifact := range artifacts {
		if err := s.Artifacts.Save(ctx, contract, artifact); err != nil {
			return SubmissionResult{}, &ProtocolError{Code: "ARTIFACT_WRITE_FAILED", Message: "artifact could not be saved", Cause: err}
		}
	}
	if status == "succeeded" {
		if err := executor.ValidateRequiredOutputs(ctx, s.DB, contract); err != nil {
			return SubmissionResult{}, &ProtocolError{Code: "REQUIRED_OUTPUT_MISSING", Message: err.Error(), Cause: err}
		}
	}
	resultJSON, err := json.Marshal(executor.Result{
		Summary: strings.TrimSpace(submission.Summary), ExecutorRef: strings.TrimSpace(submission.ExecutorRef),
	})
	if err != nil {
		return SubmissionResult{}, &ProtocolError{Code: "INVALID_RESULT", Message: "execution result cannot be encoded", Cause: err}
	}
	switch status {
	case "succeeded":
		err = s.Attempts.Complete(ctx, attemptID, row.LeaseToken, resultJSON)
	case "failed":
		code := strings.TrimSpace(submission.ErrorCode)
		if code == "" {
			code = "EXTERNAL_AGENT_FAILED"
		}
		err = s.Attempts.Fail(ctx, attemptID, row.LeaseToken, code, resultJSON)
	case "cancelled":
		err = s.Attempts.Cancel(ctx, attemptID, row.LeaseToken)
	}
	if err != nil {
		return SubmissionResult{}, &ProtocolError{Code: "ATTEMPT_TERMINAL_REJECTED", Message: "execution terminal result was rejected", Cause: err}
	}
	if err := workflowcore.FinalizeHostAttempt(ctx, s.DB, sessionID, row.StepID, attemptID, status); err != nil {
		return SubmissionResult{}, &ProtocolError{Code: "WORKFLOW_PROJECTION_FAILED", Message: "terminal execution could not update Workflow projection", Cause: err}
	}
	workflowcore.NotifyWorkflowRuntimeUpdated(ctx, s.DB, sessionID, attemptID, status)
	return SubmissionResult{ExecutionID: attemptID, AttemptStatus: status}, nil
}
