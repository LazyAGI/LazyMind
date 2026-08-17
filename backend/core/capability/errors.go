package capability

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	InvalidArgument  ErrorCode = "INVALID_ARGUMENT"
	Unauthenticated  ErrorCode = "UNAUTHENTICATED"
	PermissionDenied ErrorCode = "PERMISSION_DENIED"
	NotFound         ErrorCode = "NOT_FOUND"
	DeadlineExceeded ErrorCode = "DEADLINE_EXCEEDED"
	Unavailable      ErrorCode = "UNAVAILABLE"
	ResultTooLarge   ErrorCode = "RESULT_TOO_LARGE"
	Unsupported      ErrorCode = "UNSUPPORTED"
	Internal         ErrorCode = "INTERNAL"
)

type Error struct {
	Code      ErrorCode
	Operation string
	Message   string
	Retryable bool
	cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Operation == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Operation, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func NewError(code ErrorCode, operation, message string, retryable bool, cause error) *Error {
	return &Error{Code: code, Operation: operation, Message: message, Retryable: retryable, cause: cause}
}

func CodeOf(err error) (ErrorCode, bool) {
	var capabilityErr *Error
	if errors.As(err, &capabilityErr) {
		return capabilityErr.Code, true
	}
	return "", false
}
