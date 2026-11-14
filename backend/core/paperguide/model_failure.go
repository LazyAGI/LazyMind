package paperguide

import (
	"context"
	"errors"
	"lazymind/core/asyncjob"
)

// Classify only safe causes/classes. Parent cancellation or the run's active
// deadline takes precedence over the child model-call deadline.
func classifyModelCall(parentErr, callContextErr, callErr error) (string, error) {
	if errors.Is(parentErr, context.DeadlineExceeded) {
		return "budget", ErrBudget
	}
	if parentErr != nil {
		return "canceled", parentErr
	}
	if errors.Is(callErr, ErrBudget) {
		return "budget", ErrBudget
	}
	if errors.Is(callErr, ErrConflict) || errors.Is(callErr, asyncjob.ErrLeaseLost) {
		return "conflict", callErr
	}
	if errors.Is(callContextErr, context.DeadlineExceeded) || errors.Is(callErr, context.DeadlineExceeded) {
		return "timeout", ErrModelTimeout
	}
	if callContextErr != nil {
		return "canceled", callContextErr
	}
	if errors.Is(callErr, context.Canceled) {
		return "canceled", context.Canceled
	}
	if callErr == nil {
		return "success", nil
	}
	var classified *CallError
	if errors.As(callErr, &classified) {
		switch classified.Class {
		case "timeout":
			return "timeout", ErrModelTimeout
		case "schema", "contract":
			return classified.Class, ErrEvidence
		case "context":
			return "context", ErrContext
		case "rate_limited", "refusal":
			return classified.Class, ErrModelRequest
		}
	}
	return "transport", ErrModelRequest
}
