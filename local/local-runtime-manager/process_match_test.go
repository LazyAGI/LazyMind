package main

import (
	"path/filepath"
	"testing"
)

func TestProcessTextMatchesDesktopResourceRoot(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RepoRoot:      filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime", "app"),
		ResourcesRoot: filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime"),
		RuntimeRoot:   filepath.Join(root, "Application Support", "lazymind-desktop", "runtime"),
	}
	caddy := filepath.Join(paths.ResourcesRoot, "bin", "caddy")
	if !processTextMatchesRuntime(paths, caddy) {
		t.Fatalf("desktop resource binary should match runtime: %s", caddy)
	}
}
