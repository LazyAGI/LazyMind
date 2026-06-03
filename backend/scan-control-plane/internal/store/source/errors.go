package source

import "errors"

type ErrorCode string

const (
	ErrCodeNotFound                ErrorCode = "NOT_FOUND"
	ErrCodeSourceNotFound          ErrorCode = "SOURCE_NOT_FOUND"
	ErrCodeBindingNotFound         ErrorCode = "BINDING_NOT_FOUND"
	ErrCodeTaskNotFound            ErrorCode = "TASK_NOT_FOUND"
	ErrCodeAgentNotFound           ErrorCode = "AGENT_NOT_FOUND"
	ErrCodeBindingTargetDuplicated ErrorCode = "BINDING_TARGET_DUPLICATED"
	ErrCodeIdempotencyKeyReused    ErrorCode = "IDEMPOTENCY_KEY_REUSED"
	ErrCodeGenerationConflict      ErrorCode = "GENERATION_CONFLICT"
	ErrCodeInternal                ErrorCode = "INTERNAL_ERROR"
)

type StoreError struct {
	Code       ErrorCode
	Message    string
	Constraint string
}

func (e *StoreError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *StoreError) Is(target error) bool {
	t, ok := target.(*StoreError)
	return ok && e.Code == t.Code
}

func NewStoreError(code ErrorCode, message string) *StoreError {
	return &StoreError{Code: code, Message: message}
}

func MapConstraintError(constraint string) error {
	switch constraint {
	case "uk_source_binding_current_target":
		return &StoreError{Code: ErrCodeBindingTargetDuplicated, Constraint: constraint}
	case "uk_create_operation", "uk_parse_task_idempotency":
		return &StoreError{Code: ErrCodeIdempotencyKeyReused, Constraint: constraint}
	default:
		return &StoreError{Code: ErrCodeInternal, Constraint: constraint}
	}
}

func ErrorCodeOf(err error) ErrorCode {
	var storeErr *StoreError
	if errors.As(err, &storeErr) {
		return storeErr.Code
	}
	return ErrCodeInternal
}
