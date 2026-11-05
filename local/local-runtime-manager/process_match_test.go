package main

import (
	"path/filepath"
	"testing"
)

func TestSharedDesktopResourcesDoNotProveProcessOwnership(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RepoRoot:      filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime", "app"),
		BuildRoot:     filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime"),
		ResourcesRoot: filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime"),
		RuntimeRoot:   filepath.Join(root, "Library", "Application Support", "LazyMind"),
	}
	caddy := filepath.Join(paths.ResourcesRoot, "bin", "caddy")
	if processTextMatchesRuntime(paths, caddy) {
		t.Fatalf("shared desktop binary must not prove ownership: %s", caddy)
	}
	if !processTextMatchesRuntime(paths, caddy, "--config", filepath.Join(paths.RuntimeRoot, "generated", "Caddyfile")) {
		t.Fatal("private runtime config should identify its process")
	}
}

func TestSharedLocalBuildDoesNotProveProcessOwnership(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RepoRoot:    filepath.Join(root, "LazyMind"),
		BuildRoot:   filepath.Join(root, "LazyMind", "local", "build"),
		RuntimeRoot: filepath.Join(root, "Library", "Application Support", "LazyMind"),
	}
	manager := filepath.Join(paths.BuildRoot, "bin", "local-runtime-manager")
	if processTextMatchesRuntime(paths, manager) {
		t.Fatalf("shared local binary must not prove ownership: %s", manager)
	}
}

func TestRuntimeOwnershipUsesPrivateRootAndPreservesSharedBridge(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, BuildRoot: root, ResourcesRoot: root, RuntimeRoot: filepath.Join(root, "desktop")}
	exe := filepath.Join(root, "bin", "lazymind")
	for _, tc := range []struct {
		name      string
		args, env []string
		want      bool
	}{
		{"own worker", []string{"internal", "core-run"}, []string{runtimeRootEnvVar + "=" + paths.RuntimeRoot}, true},
		{"other runtime with same source", []string{"internal", "core-run"}, []string{runtimeRootEnvVar + "=" + filepath.Join(root, "local")}, false},
		{"other owner overrides incidental path", []string{filepath.Join(paths.RuntimeRoot, "shared-resource")}, []string{runtimeRootEnvVar + "=" + filepath.Join(root, "local")}, false},
		{"same prefix", []string{filepath.Join(paths.RuntimeRoot+"-other", "generated", "Caddyfile")}, nil, false},
		{"shared bridge", []string{"assistant", "serve"}, []string{runtimeRootEnvVar + "=" + paths.RuntimeRoot}, false},
		{"shared bridge worker", []string{"worker"}, []string{runtimeRootEnvVar + "=" + paths.RuntimeRoot, runtimeProfileEnvVar + "=bridge"}, false},
		{"explicit runtime", []string{"up", "--runtime-root", paths.RuntimeRoot}, nil, true},
		{"other explicit runtime", []string{"up", "--runtime-root", paths.RuntimeRoot + "-other"}, nil, false},
		{"explicit runtime equals syntax", []string{"up", "--runtime-root=" + paths.RuntimeRoot}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := processEnvironmentMatchesRuntime(paths, exe, tc.args, tc.env); got != tc.want {
				t.Fatalf("owned=%v want %v", got, tc.want)
			}
		})
	}
	if localProcessBelongsToRuntime(LocalProcessRecord{PID: 123, RepoRoot: root, RuntimeRoot: filepath.Join(root, "other")}, paths) {
		t.Fatal("same repository claimed another runtime's registry record")
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
