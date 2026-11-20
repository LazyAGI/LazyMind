package assistantbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
)

func TestStartupReconcilesWithoutLoginCoreOrInstalledAgents(t *testing.T) {
	user := t.TempDir()
	home := filepath.Join(user, "state")
	for name, value := range map[string]string{
		"HOME": user, "USERPROFILE": user, "APPDATA": filepath.Join(user, "roaming"),
		"LAZYMIND_HOME": home, "LAZYMIND_CONNECTOR_CONFIG": "", "LAZYMIND_RUNTIME_PROFILE": "",
		"CODEX_HOME": filepath.Join(user, "codex"), "DSH_HOME": filepath.Join(user, "dsh"), "PATH": "",
	} {
		t.Setenv(name, value)
	}
	hostID, err := agentexec.PersistentHostID()
	if err != nil {
		t.Fatal(err)
	}
	reference, err := connectorconfig.Save(connectorconfig.Config{Home: home, HostID: hostID, Provider: "raccoon"})
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(user, "deleted-old-app", "lazymind")
	entry := map[string]any{"command": old, "args": []string{"mcp", "proxy"}, "env": connectorconfig.Environment(reference), "disabled": true, "custom": "keep"}
	document := map[string]any{"mcpServers": map[string]any{"lazymind": entry, "other": map[string]any{"command": "keep"}}}
	body, _ := json.Marshal(document)
	path := filepath.Join(user, ".box-agent", "config", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileInstallations(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	servers := document["mcpServers"].(map[string]any)
	repaired := servers["lazymind"].(map[string]any)
	if repaired["command"] != connectorlauncher.Path(home) || repaired["disabled"] != true || repaired["custom"] != "keep" || servers["other"].(map[string]any)["command"] != "keep" {
		t.Fatalf("startup did not preserve and migrate the existing entry: %s", body)
	}
	for _, absent := range []string{filepath.Join(user, ".cursor", "mcp.json"), filepath.Join(user, ".workbuddy", "mcp.json"), filepath.Join(user, "codex", "config.toml")} {
		if _, err := os.Stat(absent); !os.IsNotExist(err) {
			t.Fatalf("startup created a missing integration: %s %v", absent, err)
		}
	}
	before := string(body)
	if err := ReconcileInstallations(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	if string(body) != before {
		t.Fatal("repeated startup rewrote an already current entry")
	}
}
