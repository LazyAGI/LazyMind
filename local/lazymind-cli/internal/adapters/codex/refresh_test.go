package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/mcpbridge"
)

func TestPluginRefreshUpdatesFilesWithoutDesktopLifecycleOrPairingChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	previousPrepare, previousHealth := prepareCodexDesktop, startupHealth
	prepareCodexDesktop = func(context.Context, string) error {
		t.Fatal("plugin refresh attempted desktop lifecycle control")
		return nil
	}
	startupHealth = func(context.Context, string) string {
		t.Fatal("plugin refresh probed active desktop instead of reporting saved state")
		return ""
	}
	t.Cleanup(func() { prepareCodexDesktop, startupHealth = previousPrepare, previousHealth })
	root := t.TempDir()
	home, profile := filepath.Join(root, "state"), filepath.Join(root, "profile")
	t.Setenv("HOME", root)
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv("CODEX_HOME", profile)
	bindCodexDesktopForTest(t, root)
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:1", AccessToken: "test", RefreshToken: "test"}); err != nil {
		t.Fatal(err)
	}
	self := writeExecutable(t, root, "lazymind", "#!/bin/sh\nexit 0\n")
	node := writeExecutable(t, root, "node", "#!/bin/sh\necho v22.23.0\n")
	t.Setenv("LAZYMIND_NODE_BIN", node)
	binary := writeExecutable(t, root, "codex", `#!/bin/sh
if [ "$1" = "--version" ]; then echo codex-cli 1.0.0; exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "add" ]; then
 printf '[plugins."%s"]\nenabled = true\n[mcp_servers.other]\ncommand = "other-server"\n' "$3" > "$CODEX_HOME/config.toml"
 if [ -f "$CODEX_HOME/legacy-entry" ]; then
  printf '[mcp_servers.lazymind]\ncommand = "%s/../lazymind"\nargs = ["mcp", "proxy"]\n' "$CODEX_HOME" >> "$CODEX_HOME/config.toml"
 fi
 echo installed >> "$CODEX_HOME/events"
 exit 0
fi
if [ "$1" = "mcp" ] && [ "$2" = "remove" ] && [ "$3" = "lazymind" ]; then
 rm "$CODEX_HOME/legacy-entry"
 printf '[plugins."lazymind-workflow@personal"]\nenabled = true\n[mcp_servers.other]\ncommand = "other-server"\n' > "$CODEX_HOME/config.toml"
 echo migrated >> "$CODEX_HOME/events"
 exit 0
fi
exit 1
`)
	adapter, err := New(binary, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	if result := adapter.Refresh(t.Context()); result.State != agentintegration.Failed {
		t.Fatal("refresh installed an absent plugin")
	}
	record, err := codexplugin.Install(t.Context(), home, self, binary, profile, adapter.hostID)
	if err != nil {
		t.Fatal(err)
	}
	pairBefore, _ := os.ReadFile(record.PairingFile)
	pluginRoot, _, _ := codexplugin.Paths()
	skillPath := filepath.Join(pluginRoot, "skills", "workflow", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("outdated instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	record.BuildID = "older-build"
	previous, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(pluginRoot, ".lazymind-installation.json"), previous, 0600); err != nil {
		t.Fatal(err)
	}
	if result := adapter.Refresh(t.Context()); result.State != agentintegration.Enabled || !strings.Contains(result.Message, "without restarting Codex") {
		t.Fatalf("refresh failed: %+v", result)
	}
	updated, err := codexplugin.Read()
	if err != nil || updated.BuildID != codexplugin.BuildID() || updated.ConnectorID != record.ConnectorID {
		t.Fatalf("refresh lost installation identity: %+v %v", updated, err)
	}
	skill, _ := os.ReadFile(skillPath)
	if !strings.Contains(string(skill), "external_execution") || strings.Contains(string(skill), "outdated instructions") {
		t.Fatal("refresh did not update installed skill")
	}
	pairAfter, _ := os.ReadFile(record.PairingFile)
	if string(pairAfter) != string(pairBefore) {
		t.Fatal("refresh changed live pairing")
	}
	if preference, err := integrationstate.Read(home, "codex"); err != nil || preference != nil {
		t.Fatal("refresh wrote a new enable preference")
	}
	configPath := filepath.Join(profile, "config.toml")
	current, _ := os.ReadFile(configPath)
	owned := "[mcp_servers.lazymind]\ncommand = \"" + self + "\"\nargs = [\"mcp\", \"proxy\"]\n"
	if err := os.WriteFile(configPath, append(current, []byte(owned)...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "legacy-entry"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := adapter.Refresh(t.Context()); result.State != agentintegration.Enabled {
		t.Fatalf("owned legacy migration failed: %+v", result)
	}
	current, _ = os.ReadFile(configPath)
	if strings.Contains(string(current), "mcp_servers.lazymind") || !strings.Contains(string(current), "other-server") {
		t.Fatal("refresh did not migrate only its owned legacy entry")
	}
	beforeConflict, _ := os.ReadFile(filepath.Join(profile, "events"))
	foreign := append(current, []byte("[mcp_servers.lazymind]\ncommand = \"another-installation\"\nargs = [\"mcp\", \"proxy\"]\n")...)
	if err := os.WriteFile(configPath, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if result := adapter.Refresh(t.Context()); result.State != agentintegration.Failed {
		t.Fatal("refresh replaced a foreign MCP entry")
	}
	afterConflict, _ := os.ReadFile(configPath)
	afterConflictEvents, _ := os.ReadFile(filepath.Join(profile, "events"))
	if string(afterConflict) != string(foreign) || string(beforeConflict) != string(afterConflictEvents) {
		t.Fatal("conflicting refresh changed the configuration or ran installer")
	}
	eventsBefore, _ := os.ReadFile(filepath.Join(profile, "events"))
	if err := integrationstate.Set(home, "codex", false); err != nil {
		t.Fatal(err)
	}
	if result := adapter.Refresh(t.Context()); result.State != agentintegration.Failed {
		t.Fatal("refresh enabled a disabled integration")
	}
	eventsAfter, _ := os.ReadFile(filepath.Join(profile, "events"))
	if string(eventsBefore) != string(eventsAfter) {
		t.Fatal("disabled refresh invoked the installer")
	}
}
