package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeOwnershipRejectsDifferentProfile(t *testing.T) {
	root := t.TempDir()
	state := RuntimeState{
		Profile:       "local",
		RepoRoot:      filepath.Join(root, "repo"),
		ResourcesRoot: filepath.Join(root, "repo"),
	}
	cfg := RuntimeConfig{
		Profile:       "desktop",
		OwnerToken:    "desktop-owner",
		RepoRoot:      filepath.Join(root, "resources", "app"),
		ResourcesRoot: filepath.Join(root, "resources"),
	}
	err := activeRuntimeOwnershipError(state, cfg)
	if err == nil || !strings.Contains(err.Error(), "active local runtime") {
		t.Fatalf("ownership error = %v, want active local runtime conflict", err)
	}
}

func TestDesktopRuntimeOwnershipRequiresMatchingOwner(t *testing.T) {
	root := t.TempDir()
	state := RuntimeState{
		Profile:       "desktop",
		OwnerToken:    "old-owner",
		RepoRoot:      filepath.Join(root, "runtime", "app"),
		ResourcesRoot: filepath.Join(root, "runtime"),
	}
	cfg := RuntimeConfig{
		Profile:       "desktop",
		OwnerToken:    "new-owner",
		RepoRoot:      state.RepoRoot,
		ResourcesRoot: state.ResourcesRoot,
	}
	err := activeRuntimeOwnershipError(state, cfg)
	if err == nil || !strings.Contains(err.Error(), "another application instance") {
		t.Fatalf("ownership error = %v, want owner conflict", err)
	}
	cfg.OwnerToken = state.OwnerToken
	if err := activeRuntimeOwnershipError(state, cfg); err != nil {
		t.Fatalf("matching owner rejected: %v", err)
	}
}

func TestDesktopGuardPassesOwnerTokenToShutdown(t *testing.T) {
	root := t.TempDir()
	cfg := RuntimeConfig{Profile: "desktop", OwnerToken: "guard-owner"}
	paths := RuntimePaths{RuntimeRoot: root}
	calls := 0
	err := runRuntimeGuard(
		context.Background(), cfg, paths, 99, time.Millisecond,
		func(int) bool { return false },
		func(_ context.Context, got RuntimeConfig, _ RuntimePaths) error {
			calls++
			if got.OwnerToken != cfg.OwnerToken {
				t.Fatalf("owner token = %q, want %q", got.OwnerToken, cfg.OwnerToken)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("guard failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("down calls = %d, want 1", calls)
	}
}

func TestDesktopGuardRequiresOwnerToken(t *testing.T) {
	err := runRuntimeGuard(
		context.Background(), RuntimeConfig{Profile: "desktop"}, RuntimePaths{}, 99, time.Millisecond,
		func(int) bool { return false },
		func(context.Context, RuntimeConfig, RuntimePaths) error { return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "--owner-token") {
		t.Fatalf("guard error = %v, want owner-token requirement", err)
	}
}
