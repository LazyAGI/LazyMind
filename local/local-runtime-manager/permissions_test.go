package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureComposeBindPermissionsMakesCriticalPathsReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permission bits are Unix-specific")
	}
	repo := t.TempDir()
	paths := []string{
		"db-init",
		"scripts/db-bootstrap.sh",
		"kong.yml",
		"redis-users.acl",
	}
	for _, rel := range paths {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if filepath.Ext(path) == "" {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir parent for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	if err := ensureComposeBindPermissions(repo); err != nil {
		t.Fatalf("ensure permissions: %v", err)
	}

	for _, rel := range paths {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		mode := info.Mode().Perm()
		if info.IsDir() {
			if mode&0o555 != 0o555 {
				t.Fatalf("expected directory %s to be readable/executable by containers, mode=%#o", rel, mode)
			}
			continue
		}
		if mode&0o444 != 0o444 {
			t.Fatalf("expected file %s to be readable by containers, mode=%#o", rel, mode)
		}
	}
}
