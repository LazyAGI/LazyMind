//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDirectoryLinkSupportsSpaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "directory with spaces")
	target := filepath.Join(root, "target with spaces")
	link := filepath.Join(root, "link with spaces")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createDirectoryLink(target, link); err != nil {
		t.Fatalf("create junction with spaces: %v", err)
	}
	got, ok := directoryLinkTarget(link)
	if !ok || got != target {
		t.Fatalf("junction target = %q ok=%v, want %q", got, ok, target)
	}
}
