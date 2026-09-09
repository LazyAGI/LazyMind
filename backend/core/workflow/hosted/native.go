package hosted

import (
	"context"
	"encoding/json"
	"errors"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
)

// SettleNative adapts the trusted LazyMind worker to the same atomic completion
// used by hosted agents. A worker cannot settle an external agent's attempt.
func (s *Service) SettleNative(ctx context.Context, attemptID, lease, status, code string, raw json.RawMessage) error {
	row, err := s.Attempts.Attempt(ctx, attemptID)
	if err != nil {
		return err
	}
	if row.ExecutorHost != "lazymind" || lease == "" || row.LeaseToken != lease {
		return controlstore.Reject("EXECUTOR_MISMATCH", "native execution ownership is required")
	}
	var session orm.WorkflowSession
	if err := s.DB.WithContext(ctx).First(&session, "id = ?", row.SessionID).Error; err != nil {
		return err
	}
	var result struct {
		executor.Result
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Summary == "" {
		result.Summary = result.Error
	}
	_, err = s.submitControlled(ctx, session.CreateUserID, session.ID, attemptID, Submission{
		ExecutionHandle: lease, Outcome: status, ErrorCode: code, Summary: result.Summary,
		ExecutorRef: result.ExecutorRef, Control: result.Control, Artifacts: result.Artifacts,
	})
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return controlstore.Reject(protocol.Code, protocol.Message)
	}
	return err
}
