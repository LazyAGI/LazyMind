package mcpclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkBuddySetupReadsCurrentTrustAndEnableChoices(t *testing.T) {
	// Golden approval key captured from WorkBuddy's own trust store for this
	// command/args/env-key combination, independent of any running MCP process.
	const approved = `{"528c760af0ac0498a29b20e9950990d3f5fdfffe096bc21679472f82a5a6aa57::lazymind":1792563117657}`
	for _, tc := range []struct {
		name      string
		approvals string
		mutate    func(map[string]any)
		trusted   bool
		enabled   bool
	}{
		{"approved idle connector", approved, nil, true, true},
		{"not approved", `{}`, nil, false, true},
		{"malformed approvals", `{`, nil, false, true},
		{"null approval", `{"528c760af0ac0498a29b20e9950990d3f5fdfffe096bc21679472f82a5a6aa57::lazymind":null}`, nil, false, true},
		{"disabled after approval", approved, func(e map[string]any) { e["disabled"] = true }, true, false},
		{"command changed", approved, func(e map[string]any) { e["command"] = "/new/lazymind" }, false, true},
		{"args changed", approved, func(e map[string]any) { e["args"] = []string{"mcp", "other"} }, false, true},
		{"cwd changed", approved, func(e map[string]any) { e["cwd"] = "/new" }, false, true},
		{"env key changed", approved, func(e map[string]any) { e["env"] = map[string]string{"OTHER": "value"} }, false, true},
		{"env value changed", approved, func(e map[string]any) {
			e["env"] = map[string]string{"LAZYMIND_CONNECTOR_CONFIG": "/new/connection.json"}
		}, true, true},
		{"invalid disabled value", approved, func(e map[string]any) { e["disabled"] = "true" }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mcp.json")
			entry := map[string]any{"type": "stdio", "command": "/Applications/LazyMind.app/Contents/Resources/runtime/bin/lazymind", "args": []string{"mcp", "proxy"}, "env": map[string]string{"LAZYMIND_CONNECTOR_CONFIG": "/test/connection.json"}}
			if tc.mutate != nil {
				tc.mutate(entry)
			}
			body, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"lazymind": entry}})
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "mcp-approvals.json"), []byte(tc.approvals), 0600); err != nil {
				t.Fatal(err)
			}
			trusted, enabled := workbuddySetup(path)
			if trusted != tc.trusted || enabled != tc.enabled {
				t.Fatalf("trusted=%v enabled=%v; want %v %v", trusted, enabled, tc.trusted, tc.enabled)
			}
		})
	}
}

func TestWorkBuddySetupMissingOrMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	for _, body := range []string{"", "{", `{}`, `{"mcpServers":{"other":{}}}`, `{"mcpServers":{"lazymind":null}}`} {
		if body != "" {
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if trusted, enabled := workbuddySetup(path); trusted || enabled {
			t.Fatalf("accepted missing or malformed connector: %s", body)
		}
	}
}
