package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"

	"lazymind/core/algo"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
)

const (
	recoveryRetry                   = "retry"
	recoveryRestart                 = "restart"
	recoveryNone                    = "none"
	legacyScopeAuditRejectedMessage = "scope audit rejected after repairs"
)

var errScopeAuditUnresolved = errors.New("scope audit rejected after repairs")

func organizerEffectiveErrorCode(run orm.ConversationOrganizerRun) string {
	if run.ErrorCode == "incremental_step_failed" && run.ErrorMessage == legacyScopeAuditRejectedMessage {
		return "scope_audit_unresolved"
	}
	return run.ErrorCode
}

// Only explicitly understood failures may resume a frozen checkpoint.
func recoveryForCode(code string) string {
	switch code {
	case "model_config_changed", "invalid_snapshot":
		return recoveryRestart
	case "lease_lost", "lock_expired", "database_unavailable", "model_config",
		"cancellation_unconfirmed", "connection_timeout", "response_timeout",
		"connection_error", "first_response_timeout", "stream_idle_timeout",
		"request_timeout", "transport_error", "rate_limited", "concurrency_limited",
		"provider_overloaded", "service_unavailable", "provider_internal_error",
		"authentication_failed", "permission_denied", "usage_limit_exceeded",
		"quota_exhausted", "balance_exhausted", "organization_spend_limit_exceeded",
		"project_spend_limit_exceeded", "invalid_output", "scope_audit_unresolved":
		return recoveryRetry
	default:
		return recoveryNone
	}
}

// Automatic retries are only for transient failures, not operator intervention or new proposals.
func organizerAutoRetry(code string) bool {
	switch code {
	case "lease_lost", "lock_expired", "database_unavailable", "connection_timeout", "response_timeout",
		"connection_error", "first_response_timeout", "stream_idle_timeout", "request_timeout",
		"transport_error", "rate_limited", "concurrency_limited", "provider_overloaded",
		"service_unavailable", "provider_internal_error":
		return true
	}
	return false
}

func validRecoveryCheckpoint(run orm.ConversationOrganizerRun) bool {
	var snapshot organizerSnapshot
	if json.Unmarshal(run.SnapshotJSON, &snapshot) != nil {
		return false
	}
	if len(run.PreparationJSON) > 0 {
		var preparation organizerPreparation
		if json.Unmarshal(run.PreparationJSON, &preparation) != nil {
			return false
		}
	}
	if len(run.CheckpointJSON) == 0 {
		return run.ProgressCurrent == 0
	}
	var cp incrementalCheckpoint
	if json.Unmarshal(run.CheckpointJSON, &cp) != nil {
		return false
	}
	if cp.Pending != nil && (len(cp.Pending.Assignments) == 0 || len(cp.Pending.Assignments) > 50 || cp.Pending.Operation < 0 || cp.Pending.Operation > len(cp.Pending.Operations) || cp.Pending.AuditOrdinal < -1) {
		return false
	}
	return cp.Cursor >= 0 &&
		cp.Cursor <= len(snapshot.Conversations) && cp.NextOrdinal >= 0 && cp.BatchSize >= 1 && cp.BatchSize <= 50
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
	if run.Status == "canceled" {
		return recoveryRestart
	}
	action := recoveryForCode(organizerEffectiveErrorCode(run))
	// A configuration change is a reason to create a new snapshot, even for a blocked provider error.
	config, err := modelconfig.LoadLLMConfig(ctx, db, run.UserID)
	if err != nil {
		return recoveryNone
	}
	if !sameOrganizerModelConfig(config, run.ModelConfigJSON) {
		return recoveryRestart
	}
	if action == recoveryRetry && !validRecoveryCheckpoint(run) {
		return recoveryNone
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
		return call.code, call.retryable && organizerAutoRetry(call.code)
	}
	if errors.Is(err, errCancellationUnconfirmed) {
		return "cancellation_unconfirmed", false
	}
	if errors.Is(err, errLeaseLost) {
		return "lease_lost", true
	}
	if errors.Is(err, errScopeAuditUnresolved) {
		return "scope_audit_unresolved", false
	}
	var status *algo.ConversationGroupingHTTPError
	if errors.As(err, &status) {
		code := "invalid_request"
		switch status.StatusCode {
		case 408, 504:
			code = "request_timeout"
		case 429:
			code = "rate_limited"
		case 500, 502, 503:
			code = "service_unavailable"
		case 401:
			code = "authentication_failed"
		case 403:
			code = "permission_denied"
		case 404:
			code = "not_found"
		case 409:
			code = "conflict"
		}
		return code, organizerAutoRetry(code)
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		// PostgreSQL guarantees rollback for serialization failures and deadlocks.
		switch sqlState.SQLState() {
		case "40001", "40P01", "53300", "57P03":
			return "database_unavailable", true
		}
	}
	var network net.Error
	if code != "apply_failed" && (errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF)) {
		return "transport_error", true
	}
	// Generic stage errors may represent corrupt checkpoints or programming errors.
	return code, false
}

func organizerCanRestart(run orm.ConversationOrganizerRun, recovery string) bool {
	if run.Status != "failed" && run.Status != "canceled" {
		return false
	}
	var stream organizerStream
	if len(run.StreamJSON) > 0 && json.Unmarshal(run.StreamJSON, &stream) != nil {
		return false
	}
	return recovery == recoveryRestart
}
