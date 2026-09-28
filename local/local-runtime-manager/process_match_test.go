package main

import (
	"path/filepath"
	"testing"
)

func TestProcessTextMatchesDesktopResourceRoot(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RepoRoot:      filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime", "app"),
		BuildRoot:     filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime"),
		ResourcesRoot: filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime"),
		RuntimeRoot:   filepath.Join(root, "Library", "Application Support", "LazyMind"),
	}
	caddy := filepath.Join(paths.ResourcesRoot, "bin", "caddy")
	if !processTextMatchesRuntime(paths, caddy) {
		t.Fatalf("desktop resource binary should match runtime: %s", caddy)
	}
}

func TestProcessTextMatchesLocalBuildRoot(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RepoRoot:    filepath.Join(root, "LazyMind"),
		BuildRoot:   filepath.Join(root, "LazyMind", "local", "build"),
		RuntimeRoot: filepath.Join(root, "Library", "Application Support", "LazyMind"),
	}
	manager := filepath.Join(paths.BuildRoot, "bin", "local-runtime-manager")
	if !processTextMatchesRuntime(paths, manager) {
		t.Fatalf("local build binary should match runtime: %s", manager)
	}
}

func TestIsRuntimeGuardCommandOnlyMatchesGuardInvocation(t *testing.T) {
	manager := "/tmp/LazyMind/local/build/bin/local-runtime-manager"
	if !isRuntimeGuardCommand(manager, []string{"guard", "--owner-pid", "123"}) {
		t.Fatal("guard invocation should be excluded from stale-process scanning")
	}
	if isRuntimeGuardCommand(manager, []string{"up"}) {
		t.Fatal("runtime manager up process must remain scannable")
	}
	if isRuntimeGuardCommand("/tmp/LazyMind/local/build/bin/other-manager", []string{"guard"}) {
		t.Fatal("other executables must not be treated as the runtime guard")
	}
}
