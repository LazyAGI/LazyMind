package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
)

const (
	recoveryRetry   = "retry"
	recoveryRestart = "restart"
	recoveryNone    = "none"
)

// Only explicitly understood failures may resume a frozen checkpoint.
func recoveryForCode(code string) string {
	switch code {
	case "model_config_changed", "invalid_snapshot", "model_configuration",
		"authentication_failed", "permission_denied", "not_found", "invalid_request",
		"token_limit", "input_too_large", "output_too_large", "usage_limit_exceeded",
		"quota_exhausted", "balance_exhausted", "organization_spend_limit_exceeded",
		"project_spend_limit_exceeded", "input_filtered", "output_filtered":
		return recoveryRestart
	case "lease_lost", "lock_expired", "update_failed", "model_config",
		"cancellation_unconfirmed", "connection_timeout", "response_timeout",
		"connection_error", "first_response_timeout", "stream_idle_timeout",
		"request_timeout", "transport_error", "rate_limited", "concurrency_limited",
		"provider_overloaded", "service_unavailable", "provider_internal_error":
		return recoveryRetry
	default:
		return recoveryNone
	}
}

func sameOrganizerModelConfig(config map[string]any, raw json.RawMessage) bool {
	current, err := json.Marshal(sanitizeModelConfig(config))
	if err != nil {
		return false
	}
	var a, b any
	return json.Unmarshal(current, &a) == nil && json.Unmarshal(raw, &b) == nil && reflect.DeepEqual(a, b)
}

// The DTO and retry endpoint share the same policy, including configuration drift.
func organizerRecovery(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun) string {
	if run.Status != "failed" && run.Status != "canceled" {
		return recoveryNone
	}
	var stream organizerStream
	if len(run.StreamJSON) > 0 && json.Unmarshal(run.StreamJSON, &stream) != nil {
		return recoveryNone
	}
	// Resuming the same run first confirms termination. A fresh run must not bypass this fence.
	if stream.ExecutionID != "" && !stream.Settled {
		return recoveryRetry
	}
	action := recoveryForCode(run.ErrorCode)
	if run.Status == "canceled" {
		action = recoveryRestart
	}
	if action == recoveryRetry {
		if stream.ErrorCode == run.ErrorCode && stream.Retryable != nil && !*stream.Retryable {
			return recoveryNone
		}
		config, err := modelconfig.LoadLLMConfig(ctx, db, run.UserID)
		if err != nil {
			return recoveryNone
		}
		if !sameOrganizerModelConfig(config, run.ModelConfigJSON) {
			return recoveryRestart
		}
	}
	return action
}

// Preserve the Algorithm's typed failure rather than flattening it into a stage error.
type organizerCallFailure struct {
	code      string
	retryable bool
}

func (e *organizerCallFailure) Error() string { return "organizer model call failed: " + e.code }
func failedOrganizerCall(result organizerTaskResult) error {
	code := result.ErrorCode
	if code == "" {
		code = "model_failed"
	}
	return &organizerCallFailure{code: code, retryable: result.Retryable}
}

func organizerFailure(code string, err error) (string, bool) {
	var call *organizerCallFailure
	if errors.As(err, &call) {
		return call.code, call.retryable && recoveryForCode(call.code) == recoveryRetry
	}
	if errors.Is(err, errCancellationUnconfirmed) {
		return "cancellation_unconfirmed", false
	}
	if errors.Is(err, errLeaseLost) {
		return "lease_lost", true
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "transport_error", true
	}
	// Generic stage errors may represent corrupt checkpoints or programming errors.
	return code, false
}

// Each manual retry enqueues a new job; automatic attempts reuse the same job.
// A failed manual retry offers a fresh snapshot as an alternative, after settlement.
func organizerCanRestart(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, recovery string) bool {
	if run.Status != "failed" && run.Status != "canceled" {
		return false
	}
	var stream organizerStream
	if len(run.StreamJSON) > 0 && json.Unmarshal(run.StreamJSON, &stream) != nil {
		return false
	}
	if stream.ExecutionID != "" && !stream.Settled {
		return false
	}
	if recovery == recoveryRestart {
		return true
	}
	if run.Status != "failed" || run.ErrorCode == "cancellation_unconfirmed" {
		return false
	}
	var jobs int64
	err := db.WithContext(ctx).Model(&orm.AsyncJob{}).Where("job_type=? AND resource_type=? AND resource_id=?", organizerJobType, "conversation_organizer_run", run.ID).Count(&jobs).Error
	return err == nil && jobs > 1
}
