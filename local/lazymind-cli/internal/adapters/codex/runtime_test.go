package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
)

func isolateCodexRuntime(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex-home"))
	t.Setenv("LAZYMIND_CODEX_BIN", "")
	t.Setenv(connectorconfig.EnvironmentKey, "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", filepath.Join(root, "Applications"))
	t.Setenv("SHELL", "")
	if runtime.GOOS == "windows" {
		// Registry/MSIX discovery is machine-wide; HOME and PATH isolation alone
		// must not let an installed desktop override these CLI-only fixtures.
		desktop := writeExecutable(t, root, "desktop/ChatGPT.exe", "fixture")
		if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runtimeFixture(t *testing.T, root, name, versionError, loginError string) string {
	t.Helper()
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n"
	if versionError == "" {
		body += " echo codex-cli 1.0.0; exit 0\n"
	} else {
		body += " echo '" + versionError + "' >&2; exit 1\n"
	}
	body += "fi\n"
	if loginError == "" {
		body += "echo 'Logged in using ChatGPT'\n"
	} else {
		body += "echo '" + loginError + "' >&2\nexit 1\n"
	}
	if runtime.GOOS == "windows" {
		name += ".cmd"
		body = "@echo off\r\nif \"%~1\"==\"--version\" (\r\n"
		if versionError == "" {
			body += "echo codex-cli 1.0.0\r\nexit /b 0\r\n"
		} else {
			body += "echo " + versionError + " 1>&2\r\nexit /b 1\r\n"
		}
		body += ")\r\n"
		if loginError == "" {
			body += "echo Logged in using ChatGPT\r\n"
		} else {
			body += "echo " + loginError + " 1>&2\r\nexit /b 1\r\n"
		}
	}
	return writeExecutable(t, root, name, body)
}

func TestMacCodexPrefersBundledCLIOverBrokenNpm(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bundle layout")
	}
	root := isolateCodexRuntime(t)
	bundled := runtimeFixture(t, root, "Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex", "", "")
	npm := filepath.Join(root, "npm")
	runtimeFixture(t, npm, "codex", "spawn vendor/codex ENOENT", "")
	t.Setenv("PATH", npm)
	got, err := findBinary("")
	if err != nil || !agentexec.SameExecutable(got, bundled) {
		t.Fatalf("got %q, %v; want desktop CLI %q", got, err, bundled)
	}
}

func TestCodexRuntimeSkipsBrokenAutomaticCandidate(t *testing.T) {
	root := isolateCodexRuntime(t)
	broken, good := filepath.Join(root, "broken"), filepath.Join(root, "good")
	runtimeFixture(t, broken, "codex", "spawn vendor/codex ENOENT", "")
	want := runtimeFixture(t, good, "codex", "", "")
	t.Setenv("PATH", broken+string(os.PathListSeparator)+good)
	got, err := FindBinary("")
	if err != nil || !agentexec.SameExecutable(got, want) {
		t.Fatalf("got %q, %v; want working fallback %q", got, err, want)
	}
}

func TestCodexRuntimeDoesNotReplaceBrokenExplicitOverride(t *testing.T) {
	for _, source := range []string{"argument", "environment", "binding", "connection"} {
		t.Run(source, func(t *testing.T) {
			root := isolateCodexRuntime(t)
			broken := runtimeFixture(t, root, "broken", "spawn vendor/codex ENOENT", "")
			runtimeFixture(t, filepath.Join(root, "good"), "codex", "", "")
			t.Setenv("PATH", filepath.Join(root, "good"))
			argument := ""
			switch source {
			case "argument":
				argument = broken
			case "environment":
				t.Setenv("LAZYMIND_CODEX_BIN", broken)
			case "connection":
				path, err := connectorconfig.Save(connectorconfig.Config{Home: filepath.Join(root, "state"), Provider: "codex", HostID: "runtime-test", Overrides: map[string]string{"LAZYMIND_CODEX_BIN": broken}})
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv(connectorconfig.EnvironmentKey, path)
			case "binding":
				runtimeFixture(t, root, "broken", "", "")
				if _, err := agentexec.SetExecutableBinding(agentexec.CodexCLI, broken); err != nil {
					t.Fatal(err)
				}
				runtimeFixture(t, root, "broken", "spawn vendor/codex ENOENT", "")
			}
			_, err := FindBinary(argument)
			if err == nil || !strings.Contains(err.Error(), "ENOENT") || !strings.Contains(err.Error(), filepath.Base(broken)) {
				t.Fatalf("explicit failure lost or replaced: %v", err)
			}
		})
	}
}

func TestCodexProbeDistinguishesRuntimeAndAuthenticationFailures(t *testing.T) {
	for _, tc := range []struct {
		name, versionError, loginError string
		signedOut                      bool
	}{
		{"broken launcher", "spawn vendor/codex ENOENT", "", false},
		{"login status crash", "", "spawn helper ENOENT", false},
		{"unknown authentication error", "", "credential store unreadable", false},
		{"signed out", "", "Not logged in", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := isolateCodexRuntime(t)
			binary := runtimeFixture(t, root, "codex", tc.versionError, tc.loginError)
			installed, ready, reason := Probe(binary)
			if !installed || ready || reason == "" {
				t.Fatalf("probe=%v %v %q", installed, ready, reason)
			}
			if strings.Contains(reason, "not signed in") != tc.signedOut {
				t.Fatalf("wrong authentication classification: %q", reason)
			}
			if !tc.signedOut && !strings.Contains(reason, tc.versionError+tc.loginError) {
				t.Fatalf("diagnostic lost: %q", reason)
			}
		})
	}
}

func TestCodexRuntimeDoesNotChangeInstallationForAuthentication(t *testing.T) {
	root := isolateCodexRuntime(t)
	first, last := filepath.Join(root, "first"), filepath.Join(root, "last")
	want := runtimeFixture(t, first, "codex", "", "Not logged in")
	runtimeFixture(t, last, "codex", "", "")
	t.Setenv("PATH", first+string(os.PathListSeparator)+last)
	got, err := FindBinary("")
	if err != nil || !agentexec.SameExecutable(got, want) {
		t.Fatalf("changed installation because of authentication: %q %v", got, err)
	}
	if _, err := agentexec.Run(context.Background(), got, "--version"); err != nil {
		t.Fatal(err)
	}
}

func TestMacCodexFindsBundledCLIWhenBothDesktopAppsExist(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bundle layout")
	}
	root := isolateCodexRuntime(t)
	if err := os.MkdirAll(filepath.Join(root, "Applications", "ChatGPT.app"), 0700); err != nil {
		t.Fatal(err)
	}
	want := runtimeFixture(t, root, "Applications/Codex.app/Contents/Resources/codex", "", "")
	npm := filepath.Join(root, "npm")
	runtimeFixture(t, npm, "codex", "", "")
	t.Setenv("PATH", npm)
	got, err := FindBinary("")
	if err != nil || !agentexec.SameExecutable(got, want) {
		t.Fatalf("coexisting apps selected %q, %v; want %q", got, err, want)
	}
}

func TestCodexRuntimeFallsBackAfterVersionTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX timeout fixture")
	}
	root := isolateCodexRuntime(t)
	slow, good := filepath.Join(root, "slow"), filepath.Join(root, "good")
	writeExecutable(t, slow, "codex", "#!/bin/sh\nexec /bin/sleep 10\n")
	want := runtimeFixture(t, good, "codex", "", "")
	t.Setenv("PATH", slow+string(os.PathListSeparator)+good)
	got, err := FindBinary("")
	if err != nil || !agentexec.SameExecutable(got, want) {
		t.Fatalf("timeout prevented fallback: %q %v", got, err)
	}
}

func TestCodexManagedRuntimeIgnoresStaleEnvironmentAndReloadsBinding(t *testing.T) {
	for _, mode := range []string{"connection", "desktop"} {
		t.Run(mode, func(t *testing.T) {
			root := isolateCodexRuntime(t)
			if mode == "connection" {
				path, err := connectorconfig.Save(connectorconfig.Config{Home: filepath.Join(root, "state"), Provider: "codex", HostID: "runtime-test"})
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv(connectorconfig.EnvironmentKey, path)
			} else {
				t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
			}
			t.Setenv("LAZYMIND_CODEX_BIN", filepath.Join(root, "removed-cli"))
			for _, name := range []string{"first", "updated"} {
				want := runtimeFixture(t, root, name, "", "")
				if _, err := agentexec.SetExecutableBinding(agentexec.CodexCLI, want); err != nil {
					t.Fatal(err)
				}
				for _, discover := range []func(string) (string, error){findBinary, FindBinary} {
					got, err := discover("")
					if err != nil || !agentexec.SameExecutable(got, want) {
						t.Fatalf("%s binding: got %q, %v; want %q", name, got, err, want)
					}
				}
			}
		})
	}
}
