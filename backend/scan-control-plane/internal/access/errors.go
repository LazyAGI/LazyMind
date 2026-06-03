package access

import "errors"

type ErrorCode string

const (
	ErrCodeUnauthorized ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden    ErrorCode = "FORBIDDEN"
	ErrCodeInternal     ErrorCode = "INTERNAL_ERROR"
)

type Error struct {
	Code    ErrorCode
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error {
	return e.Err
}

func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

func ErrorCodeOf(err error) ErrorCode {
	var accessErr *Error
	if errors.As(err, &accessErr) {
		return accessErr.Code
	}
	return ErrCodeInternal
}

func unauthorized(message string) error {
	return &Error{Code: ErrCodeUnauthorized, Message: message}
}

func forbidden(message string) error {
	return &Error{Code: ErrCodeForbidden, Message: message}
}

func internal(err error) error {
	return &Error{Code: ErrCodeInternal, Message: err.Error(), Err: err}
}
