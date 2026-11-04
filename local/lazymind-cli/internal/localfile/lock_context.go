package localfile

import (
	"context"
	"os"
	"time"
)

// LockContext bounds contention without leaving a goroutine waiting on an OS lock.
// The operating system releases the lock if its owning process exits.
func LockContext(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		locked, err := tryLockFile(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if locked {
			return func() { unlockFile(file); _ = file.Close() }, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
