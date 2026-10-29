package algo

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"time"

	"lazymind/core/common"
)

// WorkflowRepairTimeout bounds one repair, including sequential full-repair phases.
const WorkflowRepairTimeout = 15 * time.Minute

// WorkflowTaskError carries only safe, structured model failure metadata.
// Provider response bodies and generated content must not become UI diagnostics.
type WorkflowTaskError struct {
	Code         string `json:"error_code"`
	Retryable    bool   `json:"retryable"`
	DiagnosticID string `json:"diagnostic_id"`
}

func (e *WorkflowTaskError) Error() string {
	return "workflow AI task failed: " + e.Code
}

var workflowDiagnosticID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func workflowTaskCallError(err error) *WorkflowTaskError {
	out := &WorkflowTaskError{Code: "model_call_failed"}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		out.Code, out.Retryable = "request_timeout", true
		return out
	}
	if errors.Is(err, context.Canceled) {
		out.Code = "request_canceled"
		return out
	}
	if errors.As(err, &timeout) {
		out.Code, out.Retryable = "transport_error", true
		return out
	}
	var httpErr *common.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			out.Code, out.Retryable = "request_timeout", true
		case http.StatusBadGateway, http.StatusServiceUnavailable:
			out.Code, out.Retryable = "service_unavailable", true
		case http.StatusUnauthorized:
			out.Code = "authentication_failed"
		case http.StatusForbidden:
			out.Code = "permission_denied"
		case http.StatusTooManyRequests:
			out.Code, out.Retryable = "rate_limited", true
		}
		var envelope struct {
			Detail WorkflowTaskError `json:"detail"`
		}
		if json.Unmarshal(httpErr.Body, &envelope) == nil {
			switch envelope.Detail.Code {
			case "request_timeout", "transport_error", "authentication_failed", "permission_denied",
				"rate_limited", "concurrency_limited", "quota_exhausted", "balance_exhausted",
				"usage_limit_exceeded", "organization_spend_limit_exceeded", "project_spend_limit_exceeded",
				"provider_overloaded", "service_unavailable", "provider_internal_error", "model_configuration",
				"invalid_task_config", "invalid_model_output", "invalid_request", "not_found", "input_filtered",
				"output_filtered", "token_limit", "provider_rejected", "protocol_error":
				*out = envelope.Detail
			}
		}
		if !workflowDiagnosticID.MatchString(out.DiagnosticID) {
			out.DiagnosticID = ""
		}
	}
	return out
}
