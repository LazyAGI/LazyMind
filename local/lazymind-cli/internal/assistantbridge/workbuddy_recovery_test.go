package assistantbridge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWorkBuddyRecoveryRetriesWithoutUserActionAndStopsWhenDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	checks, attempts := 0, 0
	var logs bytes.Buffer
	keepWorkBuddyAvailable(ctx, time.Millisecond, func() bool {
		checks++
		if checks == 7 {
			cancel()
		}
		// Disabled initially, enabled for three checks, then disabled again.
		return checks >= 2 && checks <= 4
	}, func(attempt context.Context) error {
		attempts++
		if _, ok := attempt.Deadline(); !ok {
			t.Fatal("unbounded recovery")
		}
		if attempts < 3 {
			return errors.New("temporary unavailable")
		}
		return nil
	}, &logs)
	if attempts != 3 || checks != 7 {
		t.Fatalf("attempts=%d checks=%d", attempts, checks)
	}
	if strings.Count(logs.String(), "temporary unavailable") != 1 {
		t.Fatalf("repeated failure flooded logs: %s", logs.String())
	}
}

func TestWorkBuddyRecoveryFollowsDesktopLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	started := make(chan struct{})
	go func() {
		defer close(done)
		keepWorkBuddyAvailable(ctx, time.Millisecond, func() bool { return true }, func(attempt context.Context) error {
			close(started)
			<-attempt.Done()
			return attempt.Err()
		}, io.Discard)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery outlived Desktop")
	}
}
