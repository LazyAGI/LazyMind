package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAutomaticDiscoveryPrefersCompleteCodexDistribution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX executable")
	}
	root := t.TempDir()
	incomplete := filepath.Join(root, "incomplete", "codex")
	complete := filepath.Join(root, "complete", "codex")
	for _, path := range []string{incomplete, complete} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(complete), "codex-code-mode-host"),
		[]byte("#!/bin/sh\nexit 0\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(incomplete))
	t.Setenv("LAZYMIND_CODEX_BIN", "")
	resolved, err := findBinaryFromSources("", []string{"codex"}, []string{incomplete, complete})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(complete)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != expected {
		t.Fatalf("resolved=%q, want complete distribution %q", resolved, expected)
	}
}
