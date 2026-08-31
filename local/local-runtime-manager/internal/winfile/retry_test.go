package winfile

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryEventuallySucceeds(t *testing.T) {
	transient := errors.New("transient rename lock")
	attempts := 0
	err := retry(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return transient
		}
		return nil
	}, func(err error) bool {
		return errors.Is(err, transient)
	}, RetryOptions{
		MaxWait:      time.Second,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryReturnsPermanentErrorImmediately(t *testing.T) {
	permanent := errors.New("permanent rename failure")
	attempts := 0
	err := retry(context.Background(), func() error {
		attempts++
		return permanent
	}, func(error) bool { return false }, RetryOptions{})
	if !errors.Is(err, permanent) {
		t.Fatalf("error = %v, want %v", err, permanent)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
