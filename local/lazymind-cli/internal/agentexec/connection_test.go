package agentexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
)

func TestManagedProgramBindingWinsOverStaleInheritedEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOME", home)
	name, body := "codex", "#!/bin/sh\necho 1.0.0\n"
	if runtime.GOOS == "windows" {
		name = "codex.cmd"
		body = "@echo off\r\necho 1.0.0\r\n"
	}
	self := filepath.Join(home, name)
	if err := os.WriteFile(self, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := SetExecutableBinding(CodexCLI, self); err != nil {
		t.Fatal(err)
	}
	path, err := connectorconfig.Save(connectorconfig.Config{Home: home, Provider: "codex", HostID: "host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(connectorconfig.EnvironmentKey, path)
	t.Setenv("LAZYMIND_CODEX_BIN", filepath.Join(home, "removed-codex"))
	got, err := FindBoundExecutable("", "LAZYMIND_CODEX_BIN", CodexCLI, nil)
	if err != nil || !SameExecutable(got, self) {
		t.Fatalf("stale override masked binding: %s %v", got, err)
	}
	t.Setenv(connectorconfig.EnvironmentKey, "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	if _, err := FindBoundExecutable("", "LAZYMIND_CODEX_BIN", CodexCLI, nil); err == nil {
		t.Fatal("invalid explicit manual override silently ignored")
	}
}
