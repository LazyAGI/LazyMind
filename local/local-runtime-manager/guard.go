package main

import (
	"context"
	"fmt"
	"syscall"
	"time"
)

type runtimeDownFunc func(context.Context, RuntimeConfig, RuntimePaths) error
type ownerAliveFunc func(int) bool

const defaultGuardPollInterval = time.Second

func runRuntimeGuard(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, ownerPID int, pollInterval time.Duration, ownerAlive ownerAliveFunc, down runtimeDownFunc) error {
	if ownerPID <= 0 {
		return fmt.Errorf("--owner-pid must be positive")
	}
	if pollInterval <= 0 {
		pollInterval = defaultGuardPollInterval
	}
	if ownerAlive == nil {
		ownerAlive = ownerProcessAlive
	}
	if down == nil {
		return fmt.Errorf("runtime down function is required")
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if !ownerAlive(ownerPID) {
			return down(context.Background(), cfg, paths)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func ownerProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
