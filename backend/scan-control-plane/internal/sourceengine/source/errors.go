package source

import (
	"errors"
	"fmt"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type ErrorCode string

const (
	ErrCodeInvalidRequest          ErrorCode = "INVALID_REQUEST"
	ErrCodeConnectorNotFound       ErrorCode = "CONNECTOR_NOT_FOUND"
	ErrCodeInvalidTarget           ErrorCode = "INVALID_TARGET"
	ErrCodeTargetNotFound          ErrorCode = "TARGET_NOT_FOUND"
	ErrCodePermissionDenied        ErrorCode = "PERMISSION_DENIED"
	ErrCodeUnsupportedListMode     ErrorCode = "UNSUPPORTED_LIST_MODE"
	ErrCodeResultTooLarge          ErrorCode = "RESULT_TOO_LARGE"
	ErrCodeTransientSourceError    ErrorCode = "TRANSIENT_SOURCE_ERROR"
	ErrCodeBindingTargetDuplicated ErrorCode = "BINDING_TARGET_DUPLICATED"
	ErrCodeSourceVersionConflict   ErrorCode = "SOURCE_VERSION_CONFLICT"
	ErrCodeIdempotencyKeyReused    ErrorCode = "IDEMPOTENCY_KEY_REUSED"
	ErrCodeSourceNotFound          ErrorCode = "SOURCE_NOT_FOUND"
	ErrCodeBindingNotFound         ErrorCode = "BINDING_NOT_FOUND"
	ErrCodeInternal                ErrorCode = "INTERNAL_ERROR"
)

type EngineError struct {
	Code    ErrorCode
	Message string
	Details map[string]any
	Err     error
}

func (e *EngineError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *EngineError) Unwrap() error {
	return e.Err
}

func NewError(code ErrorCode, message string) *EngineError {
	return &EngineError{Code: code, Message: message}
}

func FieldError(field, reason string) *EngineError {
	return &EngineError{
		Code:    ErrCodeInvalidRequest,
		Message: fmt.Sprintf("%s: %s", field, reason),
		Details: map[string]any{
			"field":  field,
			"reason": reason,
		},
	}
}

func ErrorCodeOf(err error) ErrorCode {
	var engineErr *EngineError
	if errors.As(err, &engineErr) {
		return engineErr.Code
	}
	return ErrCodeInternal
}

func mapConnectorError(err error) error {
	if err == nil {
		return nil
	}
	code, ok := connector.ErrorCodeOf(err)
	if !ok {
		return &EngineError{Code: ErrCodeInternal, Message: err.Error(), Err: err}
	}
	switch code {
	case connector.ErrorCodeNotFound:
		return &EngineError{Code: ErrCodeConnectorNotFound, Message: err.Error(), Err: err}
	case connector.ErrorCodeInvalidTarget, connector.ErrorCodeInvalidArgument:
		return &EngineError{Code: ErrCodeInvalidTarget, Message: err.Error(), Err: err}
	case connector.ErrorCodePermissionDenied:
		return &EngineError{Code: ErrCodePermissionDenied, Message: err.Error(), Err: err}
	case connector.ErrorCodeUnsupportedListMode:
		return &EngineError{Code: ErrCodeUnsupportedListMode, Message: err.Error(), Err: err}
	case connector.ErrorCodeResultTooLarge:
		return &EngineError{Code: ErrCodeResultTooLarge, Message: err.Error(), Err: err}
	case connector.ErrorCodeTransient, connector.ErrorCodeRateLimited:
		return &EngineError{Code: ErrCodeTransientSourceError, Message: err.Error(), Err: err}
	default:
		return &EngineError{Code: ErrCodeInternal, Message: err.Error(), Err: err}
	}
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch store.ErrorCodeOf(err) {
	case store.ErrCodeSourceNotFound:
		return &EngineError{Code: ErrCodeSourceNotFound, Message: err.Error(), Err: err}
	case store.ErrCodeBindingNotFound:
		return &EngineError{Code: ErrCodeBindingNotFound, Message: err.Error(), Err: err}
	case store.ErrCodeBindingTargetDuplicated:
		return &EngineError{Code: ErrCodeBindingTargetDuplicated, Message: err.Error(), Err: err}
	case store.ErrCodeIdempotencyKeyReused:
		return &EngineError{Code: ErrCodeIdempotencyKeyReused, Message: err.Error(), Err: err}
	default:
		return err
	}
}
