package mcpclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workbuddyhost"
)

func lockWorkBuddyOperation(ctx context.Context, home string) (func(), error) {
	dir := filepath.Join(home, "workbuddy-workflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return localfile.LockContext(bounded, filepath.Join(dir, "operation.lock"))
}

func workBuddyLockError(err error) error {
	code := "configuration_failed"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "operation_busy"
	}
	if errors.Is(err, context.Canceled) {
		code = "operation_cancelled"
	}
	return &workbuddyhost.SetupError{Code: code, Err: err}
}
