package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalAuthServiceReceivesManagedGitHubCLIPath(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("local GitHub authorization is currently supported on macOS and Windows")
	}
	t.Setenv(cloudCredentialKeyEnvVar, "fixture-cloud-secret")
	t.Setenv("LAZYMIND_GITHUB_CLI_PATH", "")
	paths := RuntimePaths{BuildRoot: t.TempDir()}
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "github-auth-helper.exe"
	}
	want := "LAZYMIND_GITHUB_CLI_PATH=" + filepath.Join(paths.BuildRoot, "deps", "github-auth", runtime.GOOS+"-"+runtime.GOARCH, "bin", name)
	assertEnvContains(t, mustAuthServiceEnv(t, RuntimeConfig{Profile: "local"}, paths), want)
}

func TestDesktopAuthServiceKeepsExistingGitHubCLIResolution(t *testing.T) {
	t.Setenv(cloudCredentialKeyEnvVar, "fixture-cloud-secret")
	t.Setenv("LAZYMIND_GITHUB_CLI_PATH", "")
	for _, item := range mustAuthServiceEnv(t, RuntimeConfig{Profile: "desktop"}, RuntimePaths{BuildRoot: t.TempDir()}) {
		if strings.HasPrefix(item, "LAZYMIND_GITHUB_CLI_PATH=") {
			t.Fatalf("Desktop CLI resolution was overridden: %s", item)
		}
	}
}

func TestLocalAuthServicePreservesExplicitGitHubCLIPath(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("local GitHub authorization is currently supported on macOS and Windows")
	}
	t.Setenv(cloudCredentialKeyEnvVar, "fixture-cloud-secret")
	binary := filepath.Join(t.TempDir(), "custom-gh")
	t.Setenv("LAZYMIND_GITHUB_CLI_PATH", binary)
	paths := RuntimePaths{BuildRoot: t.TempDir()}
	assertEnvContains(t, mustAuthServiceEnv(t, RuntimeConfig{Profile: "local"}, paths), "LAZYMIND_GITHUB_CLI_PATH="+binary)
}

func TestLocalStartupContinuesWhenGitHubCLIPreparationFails(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("local GitHub authorization is currently supported on macOS and Windows")
	}
	t.Setenv("LAZYMIND_GITHUB_CLI_PATH", "")
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, BuildRoot: filepath.Join(root, "build")}
	var output bytes.Buffer
	manager := NewRuntimeManager(&fakeRunner{t: t}, "fixture-manager")
	manager.SetOutput(&output, &output)
	if err := manager.prepareStartupGitHubCLI(context.Background(), RuntimeConfig{Profile: "local"}, paths); err != nil {
		t.Fatalf("optional GitHub dependency stopped Local: %v", err)
	}
	if !strings.Contains(output.String(), "GitHub authorization tool unavailable") {
		t.Fatalf("missing preparation failure diagnostic: %s", output.String())
	}
	if _, err := os.Stat(localGitHubCLIPath(paths)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preparation published a tool: %v", err)
	}
}

func TestDesktopStartupDoesNotPrepareLocalGitHubCLI(t *testing.T) {
	t.Setenv("LAZYMIND_GITHUB_CLI_PATH", "")
	root := t.TempDir()
	var output bytes.Buffer
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, "fixture-manager")
	manager.SetOutput(&output, &output)
	if err := manager.prepareStartupGitHubCLI(context.Background(), RuntimeConfig{Profile: "desktop"}, RuntimePaths{RepoRoot: root, BuildRoot: root}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 || len(runner.calls) != 0 {
		t.Fatalf("Desktop ran Local dependency preparation: %s, %v", output.String(), runner.calls)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Desktop preparation wrote files: %v, %v", entries, err)
	}
}
