package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

func activeRuntimeOwnershipError(state RuntimeState, cfg RuntimeConfig) error {
	stateProfile := strings.TrimSpace(firstNonEmpty(state.Profile, state.Runtime))
	if stateProfile != "" && stateProfile != cfg.Profile {
		return fmt.Errorf("runtime conflict: active %s runtime must be stopped before starting or stopping %s", stateProfile, cfg.Profile)
	}
	if state.RepoRoot != "" && !sameRuntimePath(state.RepoRoot, cfg.RepoRoot) {
		return fmt.Errorf("runtime conflict: active %s runtime belongs to repository %s", stateProfile, state.RepoRoot)
	}
	if state.ResourcesRoot != "" && !sameRuntimePath(state.ResourcesRoot, cfg.ResourcesRoot) {
		return fmt.Errorf("runtime conflict: active %s runtime belongs to resources root %s", stateProfile, state.ResourcesRoot)
	}
	if cfg.Profile == "desktop" {
		if strings.TrimSpace(cfg.OwnerToken) == "" {
			return fmt.Errorf("desktop runtime requires --owner-token")
		}
		if strings.TrimSpace(state.OwnerToken) != strings.TrimSpace(cfg.OwnerToken) {
			return fmt.Errorf("runtime conflict: active desktop runtime belongs to another application instance")
		}
	}
	return nil
}

func validateRequestedRuntimeOwner(cfg RuntimeConfig) error {
	if cfg.Profile == "desktop" && strings.TrimSpace(cfg.OwnerToken) == "" {
		return fmt.Errorf("desktop runtime requires --owner-token")
	}
	return nil
}

func sameRuntimePath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
