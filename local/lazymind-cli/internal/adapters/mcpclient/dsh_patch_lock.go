package mcpclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Match @deepseek-ai/dsh-atomic-write: exclusive PID records, bounded waiting,
// and a record-specific takeover claim shared with the official DSH writer.
func lockDSHPatch(ctx context.Context, path string) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	lockPath := path + ".lock"
	delay := 20 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("DSH configuration write failed (%s): %w", lockPath, err)
		}
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_, writeErr := fmt.Fprintf(file, "%d\n", os.Getpid())
			if err := errors.Join(writeErr, file.Close(), ctx.Err()); err != nil {
				_ = os.Remove(lockPath)
				return nil, err
			}
			return func() { _ = os.Remove(lockPath) }, nil
		}
		// Windows may report permission denied for an existing exclusive file.
		_, statErr := os.Lstat(lockPath)
		if !errors.Is(err, os.ErrExist) && !(errors.Is(err, os.ErrPermission) && statErr == nil) {
			return nil, fmt.Errorf("DSH configuration write failed (%s): %w", lockPath, err)
		}
		if takeOverExitedDSHLock(lockPath) {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		delay = min(delay*2, 200*time.Millisecond)
	}
}

func exitedDSHLockHolder(record []byte) bool {
	if len(record) < 2 || record[len(record)-1] != '\n' {
		return false
	}
	for _, digit := range record[:len(record)-1] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	pid, err := strconv.ParseInt(string(record[:len(record)-1]), 10, 32)
	return err == nil && pid > 0 && int(pid) != os.Getpid() && dshProcessExited(int(pid))
}

func takeOverExitedDSHLock(path string) bool {
	record, err := os.ReadFile(path)
	if err != nil || !exitedDSHLockHolder(record) {
		return false
	}
	// All contenders (including DSH) use this exact claim name. Recheck under
	// the claim so a late contender cannot remove a newly acquired writer lock.
	sum := sha256.Sum256(record)
	claim := path + ".takeover-" + hex.EncodeToString(sum[:8])
	file, err := os.OpenFile(claim, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false
	}
	defer os.Remove(claim)
	_, writeErr := fmt.Fprintf(file, "%d\n", os.Getpid())
	if errors.Join(writeErr, file.Close()) != nil {
		return false
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, record) || !exitedDSHLockHolder(current) {
		return false
	}
	return os.Remove(path) == nil
}
