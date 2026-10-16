package executorintegration

import (
	"fmt"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFailedCodexPreparationPreservesEnablePreference(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("LAZYMIND_HOME", root)
	t.Setenv("LAZYMIND_CODEX_BIN", filepath.Join(root, "missing-codex"))
	policy, _ := executorpolicy.New(root)
	for _, previous := range []bool{false, true} {
		if _, err := policy.SetEnabled("codex", previous); err != nil {
			t.Fatal(err)
		}
		if _, err := SetEnabled(t.Context(), policy, &mcpbridge.Bridge{}, "codex", true); err == nil {
			t.Fatal("accepted missing CLI")
		}
		if enabled, err := policy.Enabled("codex"); err != nil || enabled != previous {
			t.Fatalf("changed preference after failure: %t %v", enabled, err)
		}
	}
	if _, err := SetEnabled(t.Context(), policy, &mcpbridge.Bridge{}, "codex", false); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := policy.Enabled("codex"); enabled {
		t.Fatal("disable needs broken CLI")
	}
	if _, err := os.Stat(filepath.Join(root, "plugins")); !os.IsNotExist(err) {
		t.Fatalf("discovery failure wrote plugin files: %v", err)
	}
}

func TestCodexEnableInstallsOnceAndDisableKeepsPluginAndMCP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	root := t.TempDir()
	home := filepath.Join(root, "state")
	profile := filepath.Join(root, "profile")
	t.Setenv("HOME", root)
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv("CODEX_HOME", profile)
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "codex")
	program := `#!/bin/sh
if [ "$1" = "login" ] || [ "$1" = "queue" ]; then exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "add" ]; then
 printf '\n[plugins."%s"]\nenabled = true\n' "$3" >> "$CODEX_HOME/config.toml"
 echo installed >> "$CODEX_HOME/install-events"
 exit 0
fi
exit 1
`
	if err := os.WriteFile(cli, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(root, "node")
	_ = os.WriteFile(node, []byte("#!/bin/sh\necho v22.23.0\n"), 0700)
	t.Setenv("LAZYMIND_CODEX_BIN", cli)
	t.Setenv("LAZYMIND_NODE_BIN", node)
	store, _ := credentials.NewStore(home, "")
	if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:1", AccessToken: "test", RefreshToken: "test"}); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	config := "[mcp_servers.lazymind]\ncommand = " + fmt.Sprintf("%q", self) + "\nargs = [\"mcp\", \"proxy\"]\n"
	if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	policy, _ := executorpolicy.New(home)
	for range 2 {
		if _, err := SetEnabled(t.Context(), policy, &mcpbridge.Bridge{}, "codex", true); err != nil {
			t.Fatal(err)
		}
	}
	events, _ := os.ReadFile(filepath.Join(profile, "install-events"))
	if string(events) != "installed\n" {
		t.Fatalf("not idempotent: %s", events)
	}
	if enabled, _ := policy.Enabled("codex"); !enabled {
		t.Fatal("enable not committed")
	}
	record, err := codexplugin.Read()
	if err != nil || record.BuildID != codexplugin.BuildID() || record.Profile != profile {
		t.Fatalf("plugin not prepared: %+v %v", record, err)
	}
	if _, err := SetEnabled(t.Context(), policy, &mcpbridge.Bridge{}, "codex", false); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := codexplugin.Enabled(profile, record.Marketplace); !enabled {
		t.Fatal("disable uninstalled plugin")
	}
	after, _ := os.ReadFile(filepath.Join(profile, "config.toml"))
	if !strings.Contains(string(after), config) {
		t.Fatal("CLI disable changed basic MCP")
	}
}
