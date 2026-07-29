package contract

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	InvalidArgument    ErrorCode = "INVALID_ARGUMENT"
	NotFound           ErrorCode = "NOT_FOUND"
	Conflict           ErrorCode = "CONFLICT"
	BackendUnavailable ErrorCode = "BACKEND_UNAVAILABLE"
	Unsupported        ErrorCode = "UNSUPPORTED"
	Internal           ErrorCode = "INTERNAL"
)

// Error is a protocol-independent Compat error.
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
		return e.Message
	}
	if e.Message == "" {
		return string(e.Code) + ": " + e.Operation
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

func InvalidArgumentError(operation, message string) *Error {
	return NewError(InvalidArgument, operation, message, false, nil)
}

func CodeOf(err error) (ErrorCode, bool) {
	var compatErr *Error
	if errors.As(err, &compatErr) {
		return compatErr.Code, true
	}
	return "", false
}
