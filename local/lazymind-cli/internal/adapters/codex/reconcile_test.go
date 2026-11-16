package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/mcpbridge"
)

func newPathRepairAdapter(t *testing.T) (*Adapter, string) {
	t.Helper()
	root := t.TempDir()
	home, profile := filepath.Join(root, "state"), filepath.Join(root, "codex-profile")
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv("CODEX_HOME", profile)
	t.Setenv(connectorconfig.EnvironmentKey, "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	executableName := "lazymind"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	self := writeExecutable(t, root, filepath.Join("new-installation", executableName), "unused executable fixture")
	// Discovery can fail: path repair must work without a usable Codex CLI.
	adapter, err := New(filepath.Join(root, "missing-codex"), self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, profile
}

func TestReconcileLegacyMCPWithoutCLIOrChangingNativeChoice(t *testing.T) {
	for _, format := range []string{"table", "dotted", "inline", "quoted_table"} {
		t.Run(format, func(t *testing.T) {
			adapter, profile := newPathRepairAdapter(t)
			reference, err := connectorconfig.Save(connectorconfig.Config{Home: adapter.home, HostID: adapter.hostID, Profile: profile, Provider: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			oldCommand := filepath.Join(t.TempDir(), "removed-installation", "lazymind")
			quotedOld, _ := json.Marshal(oldCommand)
			quotedRef, _ := json.Marshal(reference)
			fields := "command = " + string(quotedOld) + "\nargs = ['mcp', 'proxy']\nenabled = false\ncustom = 'keep'\n[mcp_servers.lazymind.env]\nLAZYMIND_CONNECTOR_CONFIG = " + string(quotedRef) + "\n"
			var body string
			switch format {
			case "table":
				body = "[mcp_servers.lazymind]\n" + fields
			case "quoted_table":
				body = "[mcp_servers.\"lazymind\"]\n" + fields
			case "dotted":
				body = "mcp_servers.lazymind.command = " + string(quotedOld) + " # keep comment\nmcp_servers.lazymind.args = ['mcp', 'proxy']\nmcp_servers.lazymind.enabled = false\nmcp_servers.lazymind.env.LAZYMIND_CONNECTOR_CONFIG = " + string(quotedRef) + "\n"
			case "inline":
				body = "mcp_servers = { lazymind = { command = " + string(quotedOld) + ", args = ['mcp', 'proxy'], enabled = false, env = { LAZYMIND_CONNECTOR_CONFIG = " + string(quotedRef) + " }, custom = 'keep' } }\n"
			}
			body += "# plugin disabled by user\n[plugins.\"lazymind-workflow@personal\"]\nenabled = false\n[other]\nsetting = 'preserve'\n"
			path := filepath.Join(profile, "config.toml")
			if err := os.WriteFile(path, []byte(body), 0640); err != nil {
				t.Fatal(err)
			}
			if err := adapter.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			quotedLauncher, _ := json.Marshal(connectorlauncher.Path(adapter.home))
			want := strings.Replace(body, string(quotedOld), string(quotedLauncher), 1)
			got, _ := os.ReadFile(path)
			if string(got) != want {
				t.Fatalf("expected only command replacement\ngot: %s\nwant: %s", got, want)
			}
			info, _ := os.Stat(path)
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0640 {
				t.Fatal("changed configuration file permissions")
			}
			config, exists, err := adapter.getConfig()
			if err != nil || !exists || config.Enabled || !adapter.isOwned(config) {
				t.Fatalf("legacy configuration lost ownership or native disabled state: %+v, %v", config, err)
			}
			if err := adapter.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(path)
			if string(second) != want {
				t.Fatal("repeated reconciliation changed configuration")
			}
		})
	}
}

func TestReconcileLegacyPreservesOtherInstallationAndManualEntries(t *testing.T) {
	for _, scenario := range []string{"home", "host", "profile", "provider", "manual", "args"} {
		t.Run(scenario, func(t *testing.T) {
			adapter, profile := newPathRepairAdapter(t)
			connection := connectorconfig.Config{Home: adapter.home, HostID: adapter.hostID, Profile: profile, Provider: "codex"}
			switch scenario {
			case "home":
				connection.Home = t.TempDir()
			case "host":
				connection.HostID = "another-host"
			case "profile":
				connection.Profile = t.TempDir()
			case "provider":
				connection.Provider = "workbuddy"
			}
			reference, err := connectorconfig.Save(connection)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{connectorconfig.EnvironmentKey: reference}
			if scenario == "manual" {
				env = map[string]string{}
			}
			args := []string{"mcp", "proxy"}
			if scenario == "args" {
				args = []string{"custom"}
			}
			body, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"lazymind": map[string]any{"command": filepath.Join(t.TempDir(), "removed-lazymind"), "args": args, "env": env}}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(profile, "config.toml")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if err := adapter.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(body) {
				t.Fatalf("modified %s entry", scenario)
			}
		})
	}
}

func TestStatusRepairsRemovedLegacyExecutableUsingSavedIdentity(t *testing.T) {
	adapter, profile := newPathRepairAdapter(t)
	body, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"lazymind": map[string]any{
		"command": filepath.Join(t.TempDir(), "removed-installation", "lazymind"), "args": []string{"mcp", "proxy"}, "enabled": false,
		"env": map[string]string{"LAZYMIND_HOME": adapter.home, "LAZYMIND_AGENT_HOST_ID": adapter.hostID, "LAZYMIND_AGENT_PROVIDER": "codex", "LAZYMIND_WEB_URL": "http://user-setting.example"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, "config.toml")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	status := adapter.Status(t.Context())
	if status.Issue != nil && status.Issue.Code == "environment_mismatch" {
		t.Fatal("automatic path repair still reported another environment")
	}
	config, exists, err := adapter.getConfig()
	if err != nil || !exists || config.Transport.Command != connectorlauncher.Path(adapter.home) || config.Enabled || config.Transport.Env["LAZYMIND_WEB_URL"] != "http://user-setting.example" {
		t.Fatalf("saved legacy identity was not preserved during path repair: %+v %v", config, err)
	}
}
