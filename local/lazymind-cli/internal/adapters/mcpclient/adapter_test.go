package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
)

func TestCursorStatusUsesFilesystemRequirementWithoutRunningDesktop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	adapter := testAdapter(Cursor)

	status := adapter.Status(context.Background())
	if status.State != agentintegration.RequirementsMissing {
		t.Fatalf("state=%q, want requirements_missing", status.State)
	}

	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	status = adapter.Status(context.Background())
	if status.State != agentintegration.Ready {
		t.Fatalf("state=%q, want ready", status.State)
	}
}

func TestCursorInstallURLCarriesOneManagedStdioDefinition(t *testing.T) {
	adapter := testAdapter(Cursor)
	value, err := adapter.cursorInstallURL()
	if err != nil {
		t.Fatal(err)
	}
	installURL, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if installURL.Scheme != "cursor" || installURL.Host != "anysphere.cursor-deeplink" || installURL.Path != "/mcp/install" {
		t.Fatalf("install URL=%q", value)
	}
	encoded, err := base64.StdEncoding.DecodeString(installURL.Query().Get("config"))
	if err != nil {
		t.Fatal(err)
	}
	assertStdioDefinition(t, encoded, adapter.self, adapter.home, Cursor)
}

func TestWorkBuddyUsesWorkBuddyConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := configPath(WorkBuddy)
	if path != filepath.Join(home, ".workbuddy", "mcp.json") {
		t.Fatalf("path=%q", path)
	}
	if strings.Contains(path, ".codebuddy") {
		t.Fatalf("WorkBuddy path must not target CodeBuddy: %q", path)
	}
}

func TestDeepSeekRequirementsCheckProfileAndMCPClient(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DSH_HOME", root)
	adapter := testAdapter(DeepSeekHarness)

	status := adapter.Status(context.Background())
	if status.State != agentintegration.RequirementsMissing || len(status.Requirements) != 2 {
		t.Fatalf("status=%#v", status)
	}
	writeTestFile(t, filepath.Join(root, "profiles", "web", "package.json"), `{}`)
	writeTestFile(t, filepath.Join(root, "profiles", "node_modules", "@deepseek-ai", "dsh-mcp-client", "package.json"), `{}`)
	status = adapter.Status(context.Background())
	if status.State != agentintegration.Ready {
		t.Fatalf("status=%#v", status)
	}
}

func TestManagedJSONConfigPreservesOtherServersAndRemovesOnlyLazyMind(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	self := filepath.Join(root, "bin", "lazymind")
	home := filepath.Join(root, "home")
	writeTestFile(t, self, "test connector")
	writeTestFile(t, path, `{"theme":"dark","mcpServers":{"existing":{"command":"other","args":["serve"]}}}`)

	if err := writeManagedConfig(Cursor, path, self, home, "host-1"); err != nil {
		t.Fatal(err)
	}
	state, err := readManagedConfig(Cursor, path, self, home, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.configured || !state.owned || !state.current {
		t.Fatalf("managed state=%#v", state)
	}
	var configured struct {
		Theme      string                        `json:"theme"`
		MCPServers map[string]stdioMCPDefinition `json:"mcpServers"`
	}
	body, _ := os.ReadFile(path)
	if err := json.Unmarshal(body, &configured); err != nil {
		t.Fatal(err)
	}
	if configured.Theme != "dark" || configured.MCPServers["existing"].Command != "other" {
		t.Fatalf("unrelated configuration changed: %#v", configured)
	}
	if _, err := os.Stat(path + ".lazymind-backup"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	if err := removeManagedConfig(Cursor, path); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	configured.MCPServers = nil
	if err := json.Unmarshal(body, &configured); err != nil {
		t.Fatal(err)
	}
	if _, exists := configured.MCPServers[serverName]; exists || configured.MCPServers["existing"].Command != "other" {
		t.Fatalf("unexpected servers after disconnect: %#v", configured.MCPServers)
	}
}

func TestForeignLazyMindEntryBecomesConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".workbuddy", "mcp.json")
	writeTestFile(t, path, `{"mcpServers":{"lazymind":{"command":"foreign","args":["run"]}}}`)
	adapter := testAdapter(WorkBuddy)
	status := adapter.Status(context.Background())
	if status.State != agentintegration.Conflict {
		t.Fatalf("status=%#v", status)
	}
}

func TestManagedDSHConfigPreservesOtherPatchEntries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cordis.patch.yml")
	self := filepath.Join(root, "bin", "lazymind")
	home := filepath.Join(root, "home")
	writeTestFile(t, self, "test connector")
	writeTestFile(t, path, "- insert:\n    - id: existing\n      name: existing-plugin\n      config:\n        value: keep\n")

	if err := writeManagedConfig(DeepSeekHarness, path, self, home, "host-1"); err != nil {
		t.Fatal(err)
	}
	if err := removeManagedConfig(DeepSeekHarness, path); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "id: existing") || strings.Contains(string(body), "mcp-lazymind") {
		t.Fatalf("unexpected DSH patch:\n%s", body)
	}
}

func testAdapter(kind Kind) *Adapter {
	return &Adapter{
		kind:   kind,
		self:   filepath.Join(string(filepath.Separator), "opt", "lazymind"),
		home:   filepath.Join(string(filepath.Separator), "tmp", "lazymind-home"),
		hostID: "host-1",
	}
}

func assertStdioDefinition(t *testing.T, body []byte, command, home string, kind Kind) {
	t.Helper()
	var definition stdioMCPDefinition
	if err := json.Unmarshal(body, &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Command != command || len(definition.Args) != 2 || definition.Args[0] != "mcp" || definition.Args[1] != "proxy" {
		t.Fatalf("definition=%#v", definition)
	}
	if definition.Env["LAZYMIND_HOME"] != home ||
		definition.Env["LAZYMIND_AGENT_PROVIDER"] != string(kind) ||
		definition.Env["LAZYMIND_AGENT_HOST_ID"] != "host-1" || len(definition.Env) != 3 {
		t.Fatalf("env=%#v", definition.Env)
	}
}

func writeTestFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
