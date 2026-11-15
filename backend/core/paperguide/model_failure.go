package paperguide

import (
	"context"
	"errors"
	"time"

	"lazymind/core/asyncjob"
)

// A deadline already passed at observation even if cancellation has not
// propagated yet. time.Time preserves the monotonic comparison.
func contextErrorAt(ctx context.Context, observed time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !observed.Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

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
