//go:build darwin

package agentexec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinFindUsesLoginShellPathAndPreservesItForExecution(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeDarwinExecutable(t, bin, "agent-runtime", "#!/bin/sh\nprintf 'agent 1.0.0\\n'\n")
	agent := writeDarwinExecutable(t, bin, "custom-agent", "#!/usr/bin/env agent-runtime\n")
	shell := writeDarwinExecutable(t, root, "login-shell", "#!/bin/sh\nprintf '%s\\n' \"$LAZYMIND_TEST_LOGIN_PATH\"\n")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", shell)
	t.Setenv("LAZYMIND_TEST_LOGIN_PATH", bin)

	resolved, err := Find("", []string{"custom-agent"})
	if err != nil || !SameExecutable(resolved, agent) {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}

func TestDarwinFindDiscoversCLIInsideApplicationBundle(t *testing.T) {
	applications := t.TempDir()
	binary := writeDarwinExecutable(
		t,
		filepath.Join(applications, "ChatGPT.app", "Contents", "Resources"),
		"codex",
		"#!/bin/sh\nprintf 'codex 1.0.0\\n'\n",
	)
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/usr/bin/false")

	resolved, err := Find("", []string{"codex"})
	if err != nil || !SameExecutable(resolved, binary) {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}

func TestDarwinFindDiscoversCLIInsideBoundCustomApplication(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Custom Location", "ChatGPT.app")
	binary := writeDarwinExecutable(
		t, filepath.Join(bundle, "Contents", "Resources"), "codex", "#!/bin/sh\nexit 0\n",
	)
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", filepath.Join(root, "Empty Applications"))
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/usr/bin/false")
	if _, err := SetExecutableBinding(CodexDesktop, bundle); err != nil {
		t.Fatal(err)
	}

	resolved, err := Find("", []string{"codex"})
	if err != nil || !SameExecutable(resolved, binary) {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}

func writeDarwinExecutable(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
