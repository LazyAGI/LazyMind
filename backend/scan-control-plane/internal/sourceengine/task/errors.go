package task

import (
	"errors"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type ErrorCode string

const (
	ErrCodeInvalidRequest                ErrorCode = "INVALID_REQUEST"
	ErrCodeParseBatchObjectLimitExceeded ErrorCode = "PARSE_BATCH_OBJECT_LIMIT_EXCEEDED"
	ErrCodeSourceNotFound                ErrorCode = "SOURCE_NOT_FOUND"
	ErrCodeBindingNotFound               ErrorCode = "BINDING_NOT_FOUND"
	ErrCodeTaskNotFound                  ErrorCode = "TASK_NOT_FOUND"
	ErrCodeTaskNotRetryable              ErrorCode = "TASK_NOT_RETRYABLE"
	ErrCodeTaskSuperseded                ErrorCode = "TASK_SUPERSEDED"
	ErrCodeInternal                      ErrorCode = "INTERNAL_ERROR"
)

type ServiceError struct {
	Code    ErrorCode
	Message string
	Details map[string]any
	Err     error
}

func (e *ServiceError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *ServiceError) Unwrap() error {
	return e.Err
}

func NewError(code ErrorCode, message string) *ServiceError {
	return &ServiceError{Code: code, Message: message}
}

func NewErrorWithDetails(code ErrorCode, message string, details map[string]any) *ServiceError {
	return &ServiceError{Code: code, Message: message, Details: details}
}

func ErrorCodeOf(err error) ErrorCode {
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr.Code
	}
	return ErrCodeInternal
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch store.ErrorCodeOf(err) {
	case store.ErrCodeSourceNotFound:
		return &ServiceError{Code: ErrCodeSourceNotFound, Message: err.Error(), Err: err}
	case store.ErrCodeBindingNotFound:
		return &ServiceError{Code: ErrCodeBindingNotFound, Message: err.Error(), Err: err}
	case store.ErrCodeTaskNotFound, store.ErrCodeNotFound:
		return &ServiceError{Code: ErrCodeTaskNotFound, Message: err.Error(), Err: err}
	default:
		return err
	}
}
