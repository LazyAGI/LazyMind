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

func TestCodexDesktopRecognizesChatGPTAppOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS application bundle discovery")
	}
	applications := t.TempDir()
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)
	if err := os.Mkdir(filepath.Join(applications, "ChatGPT.app"), 0o700); err != nil {
		t.Fatal(err)
	}

	requirements, err := desktopRequirements()
	if err != nil {
		t.Fatal(err)
	}
	if !agentintegration.RequirementSatisfied(requirements, "codex_desktop") {
		t.Fatalf("ChatGPT.app was not recognized as Codex Desktop: %#v", requirements)
	}
}

func TestCodexCandidatesIncludeChatGPTDesktopRuntime(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Codex Desktop runtime path")
	}
	want := "/Applications/ChatGPT.app/Contents/Resources/codex"
	for _, candidate := range codexCandidates("", "codex") {
		if candidate == want {
			return
		}
	}
	t.Fatalf("Codex candidates do not include %q", want)
}

func TestCodexStatusUsesDesktopInstallationWithoutRequiringCLILogin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex-home"))
	binary := writeExecutable(t, root, "codex", `#!/bin/sh
exit 1
`)
	desktop := writeExecutable(t, root, filepath.Join("Codex.app", "Contents", "MacOS", "Codex"), "#!/bin/sh\nexit 0\n")
	if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
		t.Fatal(err)
	}
	self := writeExecutable(t, root, filepath.Join("bin", "lazymind"), "#!/bin/sh\nexit 0\n")
	adapter, err := New(binary, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	status := adapter.Status(context.Background())
	if status.State != agentintegration.Ready {
		t.Fatalf("status=%#v", status)
	}
	if !agentintegration.RequirementSatisfied(status.Requirements, "codex_desktop") {
		t.Fatalf("desktop requirement=%#v", status.Requirements)
	}
	if agentintegration.RequirementSatisfied(status.Requirements, "codex_login") {
		t.Fatalf("desktop status unexpectedly includes CLI login: %#v", status.Requirements)
	}
}

func TestCodexStatusDoesNotTreatStandaloneCLIAsDesktop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", filepath.Join(root, "applications"))
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex-home"))
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
	if status.State != agentintegration.RequirementsMissing || status.Action != nil {
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
	binary := writeExecutable(t, root, "codex", `#!/bin/sh
if [ "$1" = "login" ] && [ "$2" = "status" ]; then exit 0; fi
if [ "$1" = "mcp" ] && [ "$2" = "list" ]; then touch "$CODEX_HOME/list-command-ran"; exit 1; fi
exit 1
`)
	self := writeExecutable(t, root, filepath.Join("bin", "lazymind"), "#!/bin/sh\nexit 0\n")
	desktop := writeExecutable(t, root, filepath.Join("Codex.app", "Contents", "MacOS", "Codex"), "#!/bin/sh\nexit 0\n")
	if _, err := agentexec.SetExecutableBinding(agentexec.CodexDesktop, desktop); err != nil {
		t.Fatal(err)
	}
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
	if status.State != agentintegration.Enabled {
		t.Fatalf("status=%#v", status)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "list-command-ran")); !os.IsNotExist(err) {
		t.Fatalf("status inspection ran Codex mcp list: %v", err)
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
