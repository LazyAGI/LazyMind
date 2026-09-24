package hosted

import (
	"context"
	"encoding/json"
	"lazymind/core/workflow/execution"
	"lazymind/core/workflow/executor"
)

// Test scenario helper: publication and completion are separate acknowledged calls.
type testCompletion struct {
	ExecutionHandle, Outcome, Summary, ErrorCode, ExecutorRef string
	Artifacts                                                 []executor.Artifact
}

func publishAndComplete(s *Service, ctx context.Context, owner, session, id string, input testCompletion) (execution.CompletionResult, error) {
	row, err := s.Attempts.Attempt(ctx, id)
	if err != nil {
		return execution.CompletionResult{}, err
	}
	handle := input.ExecutionHandle
	if handle == "" {
		handle = row.LeaseToken
	}
	if row.SubmissionHash == "" {
		for _, artifact := range input.Artifacts {
			if err := s.Publish(ctx, owner, session, id, Publication{ExecutionHandle: handle, Artifact: artifact}); err != nil {
				return execution.CompletionResult{}, err
			}
		}
	}
	return s.Complete(ctx, owner, session, id, executor.Completion{ExecutionHandle: handle, Outcome: input.Outcome, Summary: input.Summary, ErrorCode: input.ErrorCode, ExecutorRef: input.ExecutorRef})
}
func finishNative(s *Service, ctx context.Context, id, handle, status, code string, raw json.RawMessage) error {
	row, err := s.Attempts.Attempt(ctx, id)
	if err != nil {
		return err
	}
	var result executor.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	_, err = s.Completion.Complete(ctx, "owner", row.SessionID, id, executor.Completion{ExecutionHandle: handle, Outcome: status, ErrorCode: code, Summary: result.Summary, ExecutorRef: result.ExecutorRef, Control: result.Control})
	return err
}
