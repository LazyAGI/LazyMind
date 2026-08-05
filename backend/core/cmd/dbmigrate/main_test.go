package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationCreateDirRequiresReleaseForStructuredLayout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIGRATIONS_DIR", root)
	if err := os.MkdirAll(filepath.Join(root, "version_mode"), 0o755); err != nil {
		t.Fatalf("mkdir version_mode: %v", err)
	}

	if _, err := migrationCreateDir(""); err == nil {
		t.Fatal("expected missing release error")
	}
	got, err := migrationCreateDir("v0_2")
	if err != nil {
		t.Fatalf("migrationCreateDir v0_2: %v", err)
	}
	want := filepath.Join(root, "dev_mode", "v0_2")
	if got != want {
		t.Fatalf("migration create dir=%q, want %q", got, want)
	}
	if _, err := migrationCreateDir("v2"); err == nil {
		t.Fatal("expected legacy v2 release name to be rejected")
	}
}

func TestMigrationCreateDirPreservesFlatLayoutCompatibility(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIGRATIONS_DIR", root)

	got, err := migrationCreateDir("")
	if err != nil {
		t.Fatalf("migrationCreateDir: %v", err)
	}
	if got != root {
		t.Fatalf("migration create dir=%q, want %q", got, root)
	}
}
