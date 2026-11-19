package workbuddyhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
)

func TestRegisteredAcceptsFixedLauncherAndRejectsOtherBindings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	home := filepath.Join(root, "state")
	launcher := connectorlauncher.Path(home)
	if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("stable launcher"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "connector-host-id"), []byte("host-test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	for _, tc := range []struct {
		name, command, reference string
		want                     bool
	}{
		{"stable launcher", launcher, connectorconfig.Path(home, "workbuddy", ""), true},
		{"legacy current executable", self, connectorconfig.Path(home, "workbuddy", ""), true},
		{"other executable", filepath.Join(root, "missing"), connectorconfig.Path(home, "workbuddy", ""), false},
		{"other home", launcher, connectorconfig.Path(filepath.Join(root, "other"), "workbuddy", ""), false},
		{"legacy environment", launcher, "", true},
		{"legacy other host", launcher, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := connectorconfig.Environment(tc.reference)
			if tc.reference == "" {
				env = map[string]string{"LAZYMIND_HOME": home, "LAZYMIND_AGENT_PROVIDER": "workbuddy", "LAZYMIND_AGENT_HOST_ID": "host-test"}
				if tc.name == "legacy other host" {
					env["LAZYMIND_AGENT_HOST_ID"] = "other"
				}
			}
			body, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"lazymind": map[string]any{"command": tc.command, "args": []string{"mcp", "proxy"}, "env": env}}})
			path := filepath.Join(root, ".workbuddy", "mcp.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if registered(home) != tc.want {
				t.Fatal("unexpected registration state")
			}
		})
	}
}
