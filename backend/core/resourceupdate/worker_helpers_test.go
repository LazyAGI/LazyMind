package resourceupdate

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// TestRetryableOutcome creates a pending outcome with error code.
func TestRetryableOutcome(t *testing.T) {
	o := retryableOutcome("err_code", nil)
	if o.Status != orm.ResourceUpdateTaskStatusPending {
		t.Fatalf("status = %q, want pending", o.Status)
	}
	if o.ErrorCode != "err_code" {
		t.Fatalf("error_code = %q, want err_code", o.ErrorCode)
	}
	if o.Permanent {
		t.Fatal("retryable outcome should not be permanent")
	}
	if o.Deferred {
		t.Fatal("retryable outcome should not be deferred")
	}
}

// TestRetryableOutcomeWithError includes error message.
func TestRetryableOutcomeWithError(t *testing.T) {
	o := retryableOutcome("ec", assertError("boom"))
	if o.ErrorMessage != "boom" {
		t.Fatalf("message = %q, want boom", o.ErrorMessage)
	}
}

// TestDeferredOutcome creates a pending deferred outcome with retry delay.
func TestDeferredOutcome(t *testing.T) {
	o := deferredOutcome("defer_code", "try later", time.Minute)
	if o.Status != orm.ResourceUpdateTaskStatusPending {
		t.Fatalf("status = %q, want pending", o.Status)
	}
	if o.ErrorCode != "defer_code" {
		t.Fatalf("error_code = %q, want defer_code", o.ErrorCode)
	}
	if !o.Deferred {
		t.Fatal("deferred outcome should be deferred")
	}
	if o.RetryAfter != time.Minute {
		t.Fatalf("retry_after = %v, want 1m", o.RetryAfter)
	}
}

// TestPermanentOutcome creates a failed permanent outcome.
func TestPermanentOutcome(t *testing.T) {
	o := permanentOutcome("perm_code", "fatal error")
	if o.Status != orm.ResourceUpdateTaskStatusFailed {
		t.Fatalf("status = %q, want failed", o.Status)
	}
	if o.ErrorCode != "perm_code" {
		t.Fatalf("error_code = %q, want perm_code", o.ErrorCode)
	}
	if !o.Permanent {
		t.Fatal("permanent outcome should be permanent")
	}
	if o.Deferred {
		t.Fatal("permanent outcome should not be deferred")
	}
}

// TestRetryBackoff computes exponential backoff with cap.
func TestRetryBackoff(t *testing.T) {
	w := &Worker{cfg: Config{RetryBackoffBase: time.Second, RetryBackoffMax: 4 * time.Second}}

	if got := w.retryBackoff(1); got != time.Second {
		t.Fatalf("attempt 1: got %v, want 1s", got)
	}
	if got := w.retryBackoff(2); got != 2*time.Second {
		t.Fatalf("attempt 2: got %v, want 2s", got)
	}
	if got := w.retryBackoff(3); got != 4*time.Second {
		t.Fatalf("attempt 3: got %v, want 4s (capped)", got)
	}
	if got := w.retryBackoff(5); got != 4*time.Second {
		t.Fatalf("attempt 5: got %v, want 4s (capped)", got)
	}
	if got := w.retryBackoff(0); got != time.Second {
		t.Fatalf("attempt 0: got %v, want 1s (min 1)", got)
	}
}

// assertError is a helper stub that returns a concrete error.
func assertError(msg string) error {
	return &stubError{msg: msg}
}

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }
