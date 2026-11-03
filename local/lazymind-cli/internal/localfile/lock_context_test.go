package localfile

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockContextContentionAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := LockContext(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	second, err := LockContext(ctx, path)
	if second != nil {
		second()
		t.Error("second lock acquired while first held")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected deadline: %v", err)
	}
	unlock()
	third, err := LockContext(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	third()
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := LockContext(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}
