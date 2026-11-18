package codexplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
)

func pathRepairFixture(t *testing.T, enabled bool) (home, profile, reference string, record Installation) {
	t.Helper()
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("USERPROFILE", user)
	home, profile = filepath.Join(user, "state"), filepath.Join(user, "codex")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	native := "false"
	if enabled {
		native = "true"
	}
	if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte("# keep native choice\n[plugins.\"lazymind-workflow@personal\"]\nenabled = "+native+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	record = Installation{Home: home, Profile: profile, Self: filepath.Join(user, "removed-installation", "lazymind"), BuildID: "installed-old-resources", Version: "0.1.0+old", Marketplace: "personal", ConnectorID: "connector-one", PairingFile: filepath.Join(home, "workflow-hosts", "connector-one.json")}
	var err error
	reference, err = connectorconfig.Save(connectorconfig.Config{Home: home, Provider: "codex", Profile: profile, HostID: "this-host", PairingFile: record.PairingFile, Controlled: true})
	if err != nil {
		t.Fatal(err)
	}
	return
}

func writePathRepairPlugin(t *testing.T, root, reference string, record Installation) {
	t.Helper()
	data, _ := json.Marshal(record)
	var marker map[string]any
	_ = json.Unmarshal(data, &marker)
	marker["unknown_marker"] = map[string]any{"keep": "value"}
	for path, value := range map[string]any{
		".lazymind-installation.json": marker,
		".mcp.json": map[string]any{"unknown_root": true, "mcpServers": map[string]any{
			"other":    map[string]any{"command": "other-server"},
			"lazymind": map[string]any{"command": record.Self, "args": []string{"mcp", "codex-workflow"}, "env": map[string]string{connectorconfig.EnvironmentKey: reference, "USER_SETTING": "keep"}, "unknown_server": 17},
		}},
		".codex-plugin/plugin.json": map[string]any{"name": Name, "version": record.Version, "custom": "keep"},
	} {
		if err := writeJSON(filepath.Join(root, filepath.FromSlash(path)), value); err != nil {
			t.Fatal(err)
		}
	}
}

func readRepairJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReconcileRepairsOwnedSourceAndCacheWithoutChangingNativeStateOrResources(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "native_enabled", false: "native_disabled"}[enabled], func(t *testing.T) {
			home, profile, reference, record := pathRepairFixture(t, enabled)
			root, _, _ := Paths()
			cache := filepath.Join(profile, "plugins", "cache", "personal", Name, record.Version)
			writePathRepairPlugin(t, root, reference, record)
			record.BuildID = "" // Codex cached the pending installation marker.
			writePathRepairPlugin(t, cache, reference, record)
			configBefore, _ := os.ReadFile(filepath.Join(profile, "config.toml"))
			for _, path := range []string{root, cache} {
				beforeMCP := readRepairJSON(t, filepath.Join(path, ".mcp.json"))
				beforeMarker := readRepairJSON(t, filepath.Join(path, ".lazymind-installation.json"))
				manifestBefore, _ := os.ReadFile(filepath.Join(path, ".codex-plugin", "plugin.json"))
				if err := Reconcile(t.Context(), home, profile, "this-host"); err != nil {
					t.Fatal(err)
				}
				beforeMCP["mcpServers"].(map[string]any)["lazymind"].(map[string]any)["command"] = connectorlauncher.Path(home)
				beforeMarker["self"] = connectorlauncher.Path(home)
				if !reflect.DeepEqual(beforeMCP, readRepairJSON(t, filepath.Join(path, ".mcp.json"))) || !reflect.DeepEqual(beforeMarker, readRepairJSON(t, filepath.Join(path, ".lazymind-installation.json"))) {
					t.Fatal("repair changed fields beyond command and marker self")
				}
				manifestAfter, _ := os.ReadFile(filepath.Join(path, ".codex-plugin", "plugin.json"))
				if string(manifestBefore) != string(manifestAfter) {
					t.Fatal("path repair changed the installed resource version")
				}
			}
			configAfter, _ := os.ReadFile(filepath.Join(profile, "config.toml"))
			if string(configBefore) != string(configAfter) {
				t.Fatal("repair changed native configuration")
			}
			mcpBefore, _ := os.ReadFile(filepath.Join(cache, ".mcp.json"))
			if err := Reconcile(t.Context(), home, profile, "this-host"); err != nil {
				t.Fatal(err)
			}
			mcpAfter, _ := os.ReadFile(filepath.Join(cache, ".mcp.json"))
			if string(mcpBefore) != string(mcpAfter) {
				t.Fatal("repeated repair changed cached plugin content")
			}
		})
	}
}

func TestReconcilePreservesForeignOrUnregisteredPlugins(t *testing.T) {
	for _, scenario := range []string{"home", "profile", "host", "provider", "command", "args", "unregistered", "missing_marker"} {
		t.Run(scenario, func(t *testing.T) {
			home, profile, reference, record := pathRepairFixture(t, true)
			root, _, _ := Paths()
			switch scenario {
			case "home":
				record.Home = filepath.Join(t.TempDir(), "foreign")
			case "profile":
				record.Profile = t.TempDir()
			case "host", "provider":
				connection, _ := connectorconfig.Load(reference)
				if scenario == "host" {
					connection.HostID = "other-host"
				} else {
					connection.Provider = "workbuddy"
				}
				if err := writeJSON(reference, connection); err != nil {
					t.Fatal(err)
				}
			case "unregistered":
				if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte("model = 'keep'\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writePathRepairPlugin(t, root, reference, record)
			if scenario == "command" || scenario == "args" {
				value := readRepairJSON(t, filepath.Join(root, ".mcp.json"))
				server := value["mcpServers"].(map[string]any)["lazymind"].(map[string]any)
				if scenario == "command" {
					server["command"] = "/foreign-command"
				} else {
					server["args"] = []string{"mcp", "proxy"}
				}
				if err := writeJSON(filepath.Join(root, ".mcp.json"), value); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing_marker" {
				if err := os.Remove(filepath.Join(root, ".lazymind-installation.json")); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
			if err := Reconcile(t.Context(), home, profile, "this-host"); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
			if string(before) != string(after) {
				t.Fatalf("changed %s plugin", scenario)
			}
		})
	}
}
