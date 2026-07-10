package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureLocalDataRootWritableCreatesWritableDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permission bits are Unix-specific")
	}
	repo := t.TempDir()

	if err := ensureLocalDataRootWritable(repo); err != nil {
		t.Fatalf("ensure local data root writable: %v", err)
	}

	probe := filepath.Join(repo, "data", "probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		t.Fatalf("expected data root to be writable: %v", err)
	}
}
