package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalAppDataTargetIsFixedChild(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Local")
	got, err := localAppDataTarget(base)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "LazyMind"); got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
	if _, err := localAppDataTarget("relative"); err == nil {
		t.Fatal("relative Local AppData root was accepted")
	}
}

func TestPurgeLocalDataDoesNotTouchSiblingData(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "LazyMind")
	documents := filepath.Join(base, "Documents", "LazyMind")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(documents, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(documents, "keep.txt")
	if err := os.WriteFile(filepath.Join(target, "remove.txt"), []byte("remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := purgeLocalData(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("purged target still exists or stat failed unexpectedly: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("sibling user data changed: content=%q err=%v", got, err)
	}
}
