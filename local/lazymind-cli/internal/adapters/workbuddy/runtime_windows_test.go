//go:build windows

package workbuddy

import (
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
)

func TestFindRuntimeUsesWindowsWorkBuddyLayoutAndManagedNode(t *testing.T) {
	root := t.TempDir()
	application := filepath.Join(root, "WorkBuddy.exe")
	script := filepath.Join(
		root, "resources", "app.asar.unpacked", "cli", "bin", "codebuddy",
	)
	configDir := filepath.Join(root, "workbuddy-home")
	version := "22.22.2-2"
	node := filepath.Join(configDir, "binaries", "node", "versions", version, "node.exe")
	for _, path := range []string{application, script, node} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "binaries", "node", "versions", "current"),
		[]byte(version), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	if _, err := agentexec.SetExecutableBinding(agentexec.WorkBuddyDesktop, application); err != nil {
		t.Fatal(err)
	}

	command, err := findRuntime("", configDir)
	if err != nil {
		t.Fatal(err)
	}
	if !agentexec.SameExecutable(command.Binary, node) ||
		len(command.Arguments) != 1 || command.Arguments[0] != script {
		t.Fatalf("command=%#v", command)
	}
}

func TestWorkBuddyAuthenticationUsesWindowsRoamingProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	want := filepath.Join(
		root, "CodeBuddyExtension", "Data", "Public", "auth", "workbuddy-desktop.info",
	)
	if got := authFile(); got != want {
		t.Fatalf("auth file=%q want=%q", got, want)
	}
}
