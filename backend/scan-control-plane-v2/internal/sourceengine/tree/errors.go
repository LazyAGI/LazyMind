package tree

import (
	"errors"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type ErrorCode string

const (
	ErrCodeInvalidRequest      ErrorCode = "INVALID_REQUEST"
	ErrCodeConnectorNotFound   ErrorCode = "CONNECTOR_NOT_FOUND"
	ErrCodeInvalidTarget       ErrorCode = "INVALID_TARGET"
	ErrCodeUnsupportedListMode ErrorCode = "UNSUPPORTED_LIST_MODE"
	ErrCodeResultTooLarge      ErrorCode = "RESULT_TOO_LARGE"
	ErrCodeSourceNotFound      ErrorCode = "SOURCE_NOT_FOUND"
	ErrCodeBindingNotFound     ErrorCode = "BINDING_NOT_FOUND"
	ErrCodeInternal            ErrorCode = "INTERNAL_ERROR"
)

type QueryError struct {
	Code    ErrorCode
	Message string
	Details map[string]any
	Err     error
}

func (e *QueryError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *QueryError) Unwrap() error {
	return e.Err
}

func NewError(code ErrorCode, message string) *QueryError {
	return &QueryError{Code: code, Message: message}
}

func ErrorCodeOf(err error) ErrorCode {
	var queryErr *QueryError
	if errors.As(err, &queryErr) {
		return queryErr.Code
	}
	return ErrCodeInternal
}

func mapConnectorError(err error) error {
	if err == nil {
		return nil
	}
	code, ok := connector.ErrorCodeOf(err)
	if !ok {
		return &QueryError{Code: ErrCodeInternal, Message: err.Error(), Err: err}
	}
	switch code {
	case connector.ErrorCodeNotFound, "TARGET_NOT_FOUND", "OBJECT_NOT_FOUND":
		return &QueryError{Code: ErrCodeConnectorNotFound, Message: err.Error(), Err: err}
	case connector.ErrorCodeInvalidArgument, connector.ErrorCodeInvalidTarget:
		return &QueryError{Code: ErrCodeInvalidTarget, Message: err.Error(), Err: err}
	case connector.ErrorCodeUnsupportedListMode:
		return &QueryError{Code: ErrCodeUnsupportedListMode, Message: err.Error(), Err: err}
	case connector.ErrorCodeResultTooLarge:
		return &QueryError{Code: ErrCodeResultTooLarge, Message: err.Error(), Err: err}
	case "AUTH_CONNECTION_INVALID", "AGENT_NOT_AVAILABLE", "UNSUPPORTED_EXPORT", connector.ErrorCodePermissionDenied, connector.ErrorCodeRateLimited, connector.ErrorCodeTransient:
		return err
	default:
		return &QueryError{Code: ErrCodeInternal, Message: err.Error(), Err: err}
	}
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch store.ErrorCodeOf(err) {
	case store.ErrCodeSourceNotFound:
		return &QueryError{Code: ErrCodeSourceNotFound, Message: err.Error(), Err: err}
	case store.ErrCodeBindingNotFound:
		return &QueryError{Code: ErrCodeBindingNotFound, Message: err.Error(), Err: err}
	default:
		return err
	}
}
