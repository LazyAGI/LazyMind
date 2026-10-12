package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/mcpbridge"
)

func TestConfiguredCodexDiscoveryDoesNotExecuteBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	binary := writeExecutable(t, root, "codex", "#!/bin/sh\ntouch "+marker+"\n")

	resolved, err := findBinary(binary)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != expected {
		t.Fatalf("resolved=%q, want %q", resolved, expected)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("discovery executed Codex binary: %v", err)
	}
}

func TestCodexDiscoveryFindsCLIInsideWindowsDesktopInstallation(t *testing.T) {
	for _, desktopName := range []string{"ChatGPT.exe", "Codex.exe"} {
		t.Run(desktopName, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
			t.Setenv("LAZYMIND_CODEX_BIN", "")
			t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", t.TempDir())
			t.Setenv("PATH", "")
			t.Setenv("SHELL", "/usr/bin/false")
			desktop := writeExecutable(t, root, desktopName, "desktop launcher")
			cli := writeExecutable(t, root, filepath.Join("resources", "codex.exe"), "CLI")
			if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
				t.Fatal(err)
			}
			resolved, err := findBinary("")
			if err != nil || !agentexec.SameExecutable(resolved, cli) {
				t.Fatalf("resolved=%q err=%v, want bundled CLI %q", resolved, err, cli)
			}
		})
	}
}

func TestCodexDiscoveryPreservesExplicitCLIOverride(t *testing.T) {
	for _, source := range []string{"argument", "environment", "binding"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
			t.Setenv("LAZYMIND_CODEX_BIN", "")
			desktop := writeExecutable(t, root, "ChatGPT.exe", "desktop launcher")
			writeExecutable(t, root, filepath.Join("resources", "codex.exe"), "bundled CLI")
			if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
				t.Fatal(err)
			}
			name, body := "custom-codex", "#!/bin/sh\nprintf 'codex 1.0.0\\n'\n"
			if runtime.GOOS == "windows" {
				name, body = "custom-codex.cmd", "@echo off\r\necho codex 1.0.0\r\n"
			}
			cli := writeExecutable(t, root, name, body)
			argument := ""
			switch source {
			case "argument":
				argument = cli
			case "environment":
				t.Setenv("LAZYMIND_CODEX_BIN", cli)
			case "binding":
				if _, err := agentexec.SetExecutableBinding(agentexec.CodexCLI, cli); err != nil {
					t.Fatal(err)
				}
			}
			resolved, err := findBinary(argument)
			if err != nil || !agentexec.SameExecutable(resolved, cli) {
				t.Fatalf("resolved=%q err=%v, want configured CLI %q", resolved, err, cli)
			}
		})
	}
}

func TestCodexDiscoveryDoesNotUseDesktopLauncherAsCLI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("LAZYMIND_CODEX_BIN", "")
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", t.TempDir())
	t.Setenv("PATH", root)
	t.Setenv("SHELL", "/usr/bin/false")
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}
	desktop := writeExecutable(t, root, name, "desktop launcher")
	if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
		t.Fatal(err)
	}
	if resolved, err := findBinary(""); err == nil && agentexec.SameExecutable(resolved, desktop) {
		t.Fatalf("desktop launcher was accepted as CLI: %q", resolved)
	}
}

func TestCodexDiscoveryContinuesAfterDesktopLauncher(t *testing.T) {
	root := t.TempDir()
	desktopDir, cliDir := filepath.Join(root, "desktop"), filepath.Join(root, "cli")
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("LAZYMIND_CODEX_BIN", "")
	t.Setenv("PATH", desktopDir+string(os.PathListSeparator)+cliDir)
	t.Setenv("SHELL", "/usr/bin/false")
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}
	desktop := writeExecutable(t, desktopDir, name, "desktop launcher")
	cli := writeExecutable(t, cliDir, name, "CLI")
	if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
		t.Fatal(err)
	}
	resolved, err := findBinary("")
	if err != nil || !agentexec.SameExecutable(resolved, cli) {
		t.Fatalf("resolved=%q err=%v, want CLI after desktop launcher %q", resolved, err, cli)
	}
}

func TestCodexDesktopStatusDoesNotRequireCLILogin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex-home"))
	bindCodexDesktopForTest(t, root)
	binary := writeExecutable(t, root, "codex", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "list" ]; then printf '[]\n'; exit 0; fi
exit 1
`)
	self := writeExecutable(t, root, filepath.Join("bin", "lazymind"), "#!/bin/sh\nexit 0\n")
	adapter, err := New(binary, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	status := adapter.Status(context.Background())
	if status.State != agentintegration.Ready || status.Action != nil {
		t.Fatalf("status=%#v", status)
	}
	if !agentintegration.RequirementSatisfied(status.Requirements, "codex_desktop") ||
		!agentintegration.RequirementSatisfied(status.Requirements, "codex_desktop_initialized") {
		t.Fatalf("desktop requirements=%#v", status.Requirements)
	}
}

func TestCodexStatusRequiresDesktopWhenStandaloneCLIExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex-home"))
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", t.TempDir())
	binary := writeExecutable(t, root, "codex", "#!/bin/sh\nexit 0\n")
	self := writeExecutable(t, root, filepath.Join("bin", "lazymind"), "#!/bin/sh\nexit 0\n")
	adapter, err := New(binary, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	status := adapter.Status(context.Background())
	if status.State != agentintegration.RequirementsMissing ||
		agentintegration.RequirementSatisfied(status.Requirements, "codex_desktop") {
		t.Fatalf("status=%#v", status)
	}
}

func TestCodexStatusKeepsDesktopInstalledWhenCLIIsMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex-home"))
	if err := os.MkdirAll(os.Getenv("CODEX_HOME"), 0o700); err != nil {
		t.Fatal(err)
	}
	desktop := writeExecutable(t, root, "ChatGPT.exe", "test")
	if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(filepath.Join(root, "missing-codex"), self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	status := adapter.Status(context.Background())
	if status.State != agentintegration.RequirementsMissing ||
		!agentintegration.RequirementSatisfied(status.Requirements, "codex_desktop") ||
		!agentintegration.RequirementSatisfied(status.Requirements, "codex_desktop_initialized") {
		t.Fatalf("status=%#v", status)
	}
	for _, requirement := range status.Requirements {
		if requirement.ID == "codex_cli" && !requirement.Satisfied {
			return
		}
	}
	t.Fatalf("missing CLI requirement not reported: %#v", status.Requirements)
}

func TestCodexStatusDiscoversDesktopBundleWithoutCLILocationRules(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS application bundle fixture")
	}
	root := t.TempDir()
	applications := filepath.Join(root, "Applications")
	codexHome := filepath.Join(root, "codex-home")
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/usr/bin/false")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(
		t,
		filepath.Join(applications, "ChatGPT.app", "Contents", "Resources", "codex-cli", "bin"),
		"codex",
		"#!/bin/sh\nexit 0\n",
	)
	self := writeExecutable(t, root, filepath.Join("bin", "lazymind"), "#!/bin/sh\nexit 0\n")
	adapter, err := New("", self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	status := adapter.Status(context.Background())
	if status.State != agentintegration.Ready ||
		!agentintegration.RequirementSatisfied(status.Requirements, "codex_desktop") {
		t.Fatalf("status=%#v", status)
	}
}

func TestCodexStatusReadsManagedMCPFromConfigWithoutLaunchingListCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	stateHome := filepath.Join(root, "state")
	codexHome := filepath.Join(root, "codex-home")
	t.Setenv("LAZYMIND_HOME", stateHome)
	t.Setenv("CODEX_HOME", codexHome)
	bindCodexDesktopForTest(t, root)
	binary := writeExecutable(t, root, "codex", `#!/bin/sh
if [ "$1" = "login" ] && [ "$2" = "status" ]; then exit 0; fi
if [ "$1" = "mcp" ] && [ "$2" = "list" ]; then touch "$CODEX_HOME/list-command-ran"; exit 1; fi
exit 1
`)
	self := writeExecutable(t, root, filepath.Join("bin", "lazymind"), "#!/bin/sh\nexit 0\n")
	adapter, err := New(binary, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	config := `[mcp_servers.lazymind]
command = "` + self + `"
args = ["mcp", "proxy"]

[mcp_servers.lazymind.env]
LAZYMIND_HOME = "` + stateHome + `"
LAZYMIND_AGENT_PROVIDER = "codex"
LAZYMIND_AGENT_HOST_ID = "` + adapter.hostID + `"
`
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	status := adapter.Status(context.Background())
	if status.State != agentintegration.Ready {
		t.Fatalf("status=%#v", status)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "list-command-ran")); !os.IsNotExist(err) {
		t.Fatalf("status inspection ran Codex mcp list: %v", err)
	}
}

func bindCodexDesktopForTest(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(os.Getenv("CODEX_HOME"), 0o700); err != nil {
		t.Fatal(err)
	}
	desktop := writeExecutable(t, root, "Codex Desktop", "test")
	if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, root, name, body string) string {
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
