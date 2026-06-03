package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/lazymind/scan_control_plane/internal/access"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/tree"
)

type ErrorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	err := decodeJSON(r, dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func invalidJSON(err error) *sourceengine.EngineError {
	return &sourceengine.EngineError{
		Code:    sourceengine.ErrCodeInvalidRequest,
		Message: "invalid JSON request body",
		Details: map[string]any{
			"reason": err.Error(),
		},
	}
}

func writeError(w http.ResponseWriter, err error) {
	code, message, details := errorPayload(err)
	writeJSON(w, statusForCode(code), ErrorResponse{Code: code, Message: message, Details: details})
}

func errorPayload(err error) (string, string, map[string]any) {
	if err == nil {
		return "INTERNAL_ERROR", "internal error", emptyDetails()
	}
	var sourceErr *sourceengine.EngineError
	if errors.As(err, &sourceErr) {
		return string(sourceErr.Code), sourceErr.Error(), detailsOrEmpty(sourceErr.Details)
	}
	var accessErr *access.Error
	if errors.As(err, &accessErr) {
		return string(accessErr.Code), accessErr.Error(), emptyDetails()
	}
	var treeErr *tree.QueryError
	if errors.As(err, &treeErr) {
		return string(treeErr.Code), treeErr.Error(), detailsOrEmpty(treeErr.Details)
	}
	var taskErr *taskengine.ServiceError
	if errors.As(err, &taskErr) {
		return string(taskErr.Code), taskErr.Error(), detailsOrEmpty(taskErr.Details)
	}
	var connectorErr *connector.ConnectorError
	if errors.As(err, &connectorErr) {
		return connectorHTTPCode(connectorErr.Code), connectorErr.Error(), emptyDetails()
	}
	return "INTERNAL_ERROR", err.Error(), emptyDetails()
}

func detailsOrEmpty(details map[string]any) map[string]any {
	if details == nil {
		return emptyDetails()
	}
	return details
}

func emptyDetails() map[string]any {
	return map[string]any{}
}

func connectorHTTPCode(code connector.ErrorCode) string {
	switch code {
	case "AUTH_CONNECTION_INVALID", "AGENT_NOT_AVAILABLE", "UNSUPPORTED_EXPORT", "TARGET_NOT_FOUND", "OBJECT_NOT_FOUND":
		return string(code)
	case connector.ErrorCodeNotFound:
		return "CONNECTOR_NOT_FOUND"
	case connector.ErrorCodeInvalidArgument, connector.ErrorCodeInvalidTarget:
		return "INVALID_TARGET"
	case connector.ErrorCodePermissionDenied:
		return "PERMISSION_DENIED"
	case connector.ErrorCodeUnsupportedListMode:
		return "UNSUPPORTED_LIST_MODE"
	case connector.ErrorCodeResultTooLarge:
		return "RESULT_TOO_LARGE"
	case connector.ErrorCodeRateLimited:
		return "RATE_LIMITED"
	case connector.ErrorCodeTransient:
		return "TRANSIENT_SOURCE_ERROR"
	default:
		return "INTERNAL_ERROR"
	}
}

func statusForCode(code string) int {
	switch code {
	case "INVALID_REQUEST", "PARSE_BATCH_OBJECT_LIMIT_EXCEEDED", "CONNECTOR_NOT_FOUND", "INVALID_TARGET", "UNSUPPORTED_LIST_MODE", "UNSUPPORTED_EXPORT":
		return http.StatusBadRequest
	case "UNAUTHORIZED", "AUTH_CONNECTION_INVALID":
		return http.StatusUnauthorized
	case "FORBIDDEN", "PERMISSION_DENIED":
		return http.StatusForbidden
	case "NOT_FOUND", "SOURCE_NOT_FOUND", "BINDING_NOT_FOUND", "TASK_NOT_FOUND", "TARGET_NOT_FOUND", "OBJECT_NOT_FOUND":
		return http.StatusNotFound
	case "BINDING_TARGET_DUPLICATED", "SOURCE_VERSION_CONFLICT", "IDEMPOTENCY_KEY_REUSED", "GENERATION_CONFLICT", "TASK_NOT_RETRYABLE", "TASK_SUPERSEDED":
		return http.StatusConflict
	case "RESULT_TOO_LARGE":
		return http.StatusRequestEntityTooLarge
	case "RATE_LIMITED":
		return http.StatusTooManyRequests
	case "AGENT_NOT_AVAILABLE", "TRANSIENT_SOURCE_ERROR":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func missingDependency(name string) error {
	return sourceengine.NewError(sourceengine.ErrCodeInternal, name+" is not configured")
}
