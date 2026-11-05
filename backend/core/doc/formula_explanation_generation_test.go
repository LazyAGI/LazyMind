package doc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormulaGenerationFastPathUsesOneAttempt(t *testing.T) {
	var calls atomic.Int32
	result, err := runFormulaGeneration(t.Context(), func(context.Context) (formulaExplanation, error) {
		calls.Add(1)
		return formulaExplanation{Summary: "ready"}, nil
	}, make(chan time.Time))
	if err != nil || result.Summary != "ready" || calls.Load() != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestFormulaGenerationSlowAttemptUsesOneBackup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tick := make(chan time.Time, 1)
	started := make(chan struct{}, 1)
	stopped := make(chan struct{})
	var calls atomic.Int32
	go func() { <-started; tick <- time.Now() }()
	result, err := runFormulaGeneration(ctx, func(ctx context.Context) (formulaExplanation, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-ctx.Done()
			close(stopped)
			return formulaExplanation{}, ctx.Err()
		}
		return formulaExplanation{Summary: "backup"}, nil
	}, tick)
	if err != nil || result.Summary != "backup" || calls.Load() != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("losing call was not cancelled")
	}
}

func TestFormulaGenerationInvalidAttemptCannotWin(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tick := make(chan time.Time, 1)
	started := make(chan struct{})
	second := make(chan struct{})
	var calls atomic.Int32
	go func() { <-started; tick <- time.Now() }()
	result, err := runFormulaGeneration(ctx, func(context.Context) (formulaExplanation, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-second
			return formulaExplanation{}, errors.New("invalid response")
		}
		close(second)
		return formulaExplanation{Summary: "valid"}, nil
	}, tick)
	if err != nil || result.Summary != "valid" || calls.Load() != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestFormulaGenerationFailureDoesNotExceedTwoAttempts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tick := make(chan time.Time, 1)
	started := make(chan struct{})
	second := make(chan struct{})
	var calls atomic.Int32
	go func() { <-started; tick <- time.Now() }()
	_, err := runFormulaGeneration(ctx, func(context.Context) (formulaExplanation, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-second
		} else {
			close(second)
		}
		return formulaExplanation{}, errors.New("unavailable")
	}, tick)
	if err == nil || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestFormulaGenerationAlreadyCancelledDoesNotCallModel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var calls atomic.Int32
	_, err := runFormulaGeneration(ctx, func(context.Context) (formulaExplanation, error) { calls.Add(1); return formulaExplanation{}, nil }, make(chan time.Time))
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestFormulaGenerationFirstValidResultCanStillWin(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tick := make(chan time.Time, 1)
	started := make(chan struct{})
	second := make(chan struct{})
	stopped := make(chan struct{})
	var calls atomic.Int32
	go func() { <-started; tick <- time.Now() }()
	result, err := runFormulaGeneration(ctx, func(ctx context.Context) (formulaExplanation, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-second
			return formulaExplanation{Summary: "first"}, nil
		}
		close(second)
		<-ctx.Done()
		close(stopped)
		return formulaExplanation{}, ctx.Err()
	}, tick)
	if err != nil || result.Summary != "first" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("backup was not cancelled")
	}
}

func TestFormulaGenerationCancellationBeforeBackup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	_, err := runFormulaGeneration(ctx, func(ctx context.Context) (formulaExplanation, error) {
		calls.Add(1)
		cancel()
		return formulaExplanation{}, ctx.Err()
	}, make(chan time.Time))
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestFormulaGenerationRetriesEarlyInvalidResultOnce(t *testing.T) {
	var calls atomic.Int32
	result, err := runFormulaGeneration(t.Context(), func(context.Context) (formulaExplanation, error) {
		if calls.Add(1) == 1 {
			return formulaExplanation{}, errors.New("invalid symbol")
		}
		return formulaExplanation{Summary: "complete"}, nil
	}, make(chan time.Time))
	if err != nil || result.Summary != "complete" || calls.Load() != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
}
