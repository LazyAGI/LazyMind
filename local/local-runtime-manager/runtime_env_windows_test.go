//go:build windows

package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultHostCacheDirUsesRelocatedLocalAppData(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	relocated := filepath.Join(t.TempDir(), "relocated-local-app-data")
	t.Setenv("LOCALAPPDATA", relocated)
	if got := defaultHostCacheDir(home); got != relocated {
		t.Fatalf("default host cache dir = %q, want relocated LOCALAPPDATA %q", got, relocated)
	}
}
