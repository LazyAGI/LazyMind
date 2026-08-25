package mcpclient

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorGuideUsesOfficialInstallLinkAndMCPFileFallback(t *testing.T) {
	t.Setenv("LAZYMIND_HOME", filepath.Join(t.TempDir(), "LazyMind Home"))
	adapter := testAdapter(Cursor)
	guide, err := adapter.setupGuide()
	if err != nil {
		t.Fatal(err)
	}
	if guide.Method != SetupCursorInstall || guide.URL == "" {
		t.Fatalf("Cursor setup guide = %#v", guide)
	}
	installURL, err := url.Parse(guide.URL)
	if err != nil {
		t.Fatal(err)
	}
	if installURL.Scheme != "https" || installURL.Host != "cursor.com" || installURL.Path != "/en/install-mcp" {
		t.Fatalf("Cursor install URL = %s", guide.URL)
	}
	if installURL.Query().Get("name") != serverName {
		t.Fatalf("Cursor install name = %q", installURL.Query().Get("name"))
	}
	encoded, err := base64.StdEncoding.DecodeString(installURL.Query().Get("config"))
	if err != nil {
		t.Fatal(err)
	}
	assertStdioDefinition(t, encoded, adapter.self, adapter.home, Cursor)
	assertMCPFile(t, guide.Configuration, adapter.self, adapter.home, Cursor)
	if !strings.HasSuffix(guide.ConfigPath, filepath.Join(".cursor", "mcp.json")) {
		t.Fatalf("Cursor fallback path = %q", guide.ConfigPath)
	}
}

func TestWorkBuddyGuideUsesMCPFileConfiguration(t *testing.T) {
	t.Setenv("LAZYMIND_HOME", filepath.Join(t.TempDir(), "LazyMind Home"))
	adapter := testAdapter(WorkBuddy)
	guide, err := adapter.setupGuide()
	if err != nil {
		t.Fatal(err)
	}
	if guide.Method != SetupConfigFile || guide.URL != "" {
		t.Fatalf("WorkBuddy setup guide = %#v", guide)
	}
	assertMCPFile(t, guide.Configuration, adapter.self, adapter.home, WorkBuddy)
	if !strings.HasSuffix(guide.ConfigPath, filepath.Join(".codebuddy", "mcp.json")) {
		t.Fatalf("WorkBuddy config path = %q", guide.ConfigPath)
	}
}

func TestTRAEWorkGuideUsesAgentMCPFile(t *testing.T) {
	t.Setenv("LAZYMIND_HOME", filepath.Join(t.TempDir(), "LazyMind Home"))
	adapter := testAdapter(TRAEWork)
	guide, err := adapter.setupGuide()
	if err != nil {
		t.Fatal(err)
	}
	if guide.Method != SetupTRAEConfigFile || !strings.HasSuffix(guide.ConfigPath, filepath.Join("TRAE SOLO CN", "User", "mcp.json")) {
		t.Fatalf("TRAE Work setup guide = %#v", guide)
	}
	assertMCPFile(t, guide.Configuration, adapter.self, adapter.home, TRAEWork)
}

func TestDeepSeekHarnessGuideUsesWebProfilePatch(t *testing.T) {
	t.Setenv("DSH_HOME", filepath.Join(t.TempDir(), "DeepSeek Harness Home"))
	t.Setenv("LAZYMIND_HOME", filepath.Join(t.TempDir(), "LazyMind Home"))
	adapter := testAdapter(DeepSeekHarness)
	guide, err := adapter.setupGuide()
	if err != nil {
		t.Fatal(err)
	}
	if guide.Method != SetupDSHProfilePatch || guide.ConfigPath != filepath.Join(os.Getenv("DSH_HOME"), "profiles", "web", "cordis.patch.yml") {
		t.Fatalf("DeepSeek Harness setup guide = %#v", guide)
	}
	for _, required := range []string{
		"- insert:", "id: mcp-lazymind", "name: '@deepseek-ai/dsh-mcp-client'",
		"serverName: lazymind", "transport: stdio", "command: " + jsonQuote(adapter.self),
		"args: ['mcp', 'proxy']", "LAZYMIND_HOME: " + jsonQuote(adapter.home),
		"LAZYMIND_AGENT_PROVIDER: " + jsonQuote(string(DeepSeekHarness)),
		"LAZYMIND_AGENT_HOST_ID: " + jsonQuote(adapter.hostID),
		"failOnStartupError: true",
	} {
		if !strings.Contains(guide.Configuration, required) {
			t.Fatalf("DeepSeek Harness patch lacks %q:\n%s", required, guide.Configuration)
		}
	}
}

func TestDiscoverDeepSeekHarnessFromNpxWebProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DSH_HOME", root)
	writeTestFile(t, filepath.Join(root, "profiles", "web", "package.json"), `{}`)
	writeTestFile(t, filepath.Join(root, "profiles", "node_modules", "@deepseek-ai", "dsh-mcp-client", "package.json"), `{}`)
	writeTestFile(t, filepath.Join(root, "profiles", "node_modules", "@deepseek-ai", "dsh", "package.json"), `{"version":"0.1.0-rc.6"}`)
	profile, version, err := discoverDSHProfile()
	if err != nil {
		t.Fatal(err)
	}
	if profile != filepath.Join(root, "profiles", "web", "package.json") || version != "0.1.0-rc.6" {
		t.Fatalf("DeepSeek Harness discovery = %q, %q", profile, version)
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
		t.Fatalf("managed state = %#v", state)
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
		t.Fatalf("configuration backup missing: %v", err)
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
		t.Fatalf("disconnect changed unrelated configuration: %#v", configured.MCPServers)
	}
}

func TestManagedJSONConfigRejectsForeignLazyMindEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{"mcpServers":{"lazymind":{"command":"foreign","args":["run"]}}}`)
	state, err := readManagedConfig(WorkBuddy, path, "/owned/lazymind", "", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.configured || state.owned {
		t.Fatalf("foreign state = %#v", state)
	}
}

func TestManagedConfigsUpgradeLegacyDefaultHome(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	self := filepath.Join(userHome, "bin", "lazymind")
	home := filepath.Join(userHome, ".lazymind")
	writeTestFile(t, self, "test connector")

	tests := []struct {
		kind Kind
		path string
		body string
	}{
		{
			kind: Cursor,
			path: filepath.Join(userHome, ".cursor", "mcp.json"),
			body: `{"mcpServers":{"lazymind":{"type":"stdio","command":` + jsonQuote(self) + `,"args":["mcp","proxy"],"env":{"LAZYMIND_AGENT_PROVIDER":"cursor"}}}}`,
		},
		{
			kind: DeepSeekHarness,
			path: filepath.Join(userHome, ".dsh", "profiles", "web", "cordis.patch.yml"),
			body: `[{insert: [{id: mcp-lazymind, name: '@deepseek-ai/dsh-mcp-client', config: {serverName: lazymind, transport: stdio, command: ` + jsonQuote(self) + `, args: ['mcp', 'proxy'], env: {LAZYMIND_AGENT_PROVIDER: "deepseek-harness"}, failOnStartupError: true}}]}]`,
		},
	}

	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			writeTestFile(t, test.path, test.body)
			legacy, err := readManagedConfig(test.kind, test.path, self, home, "host-1")
			if err != nil {
				t.Fatal(err)
			}
			if !legacy.configured || !legacy.owned || legacy.current {
				t.Fatalf("legacy state = %#v", legacy)
			}
			if err := writeManagedConfig(test.kind, test.path, self, home, "host-1"); err != nil {
				t.Fatal(err)
			}
			upgraded, err := readManagedConfig(test.kind, test.path, self, home, "host-1")
			if err != nil {
				t.Fatal(err)
			}
			if !upgraded.configured || !upgraded.owned || !upgraded.current {
				t.Fatalf("upgraded state = %#v", upgraded)
			}
		})
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
	state, err := readManagedConfig(DeepSeekHarness, path, self, home, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.configured || !state.owned {
		t.Fatalf("managed DSH state = %#v", state)
	}
	if err := removeManagedConfig(DeepSeekHarness, path); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "id: existing") || strings.Contains(string(body), "mcp-lazymind") {
		t.Fatalf("unexpected DSH patch after disconnect:\n%s", body)
	}
}

func testAdapter(kind Kind) *Adapter {
	return &Adapter{
		kind: kind, definition: definition{agent: string(kind)},
		binary: "/Applications/Agent.app/Contents/MacOS/agent",
		self:   "/Applications/LazyMind.app/Contents/Resources/runtime/bin/lazymind",
		home:   os.Getenv("LAZYMIND_HOME"),
		hostID: "host-1",
	}
}

func assertMCPFile(t *testing.T, value, command, home string, kind Kind) {
	t.Helper()
	var configuration mcpFile
	if err := json.Unmarshal([]byte(value), &configuration); err != nil {
		t.Fatal(err)
	}
	definition, exists := configuration.MCPServers[serverName]
	if !exists {
		t.Fatalf("MCP file has no %q server", serverName)
	}
	body, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	assertStdioDefinition(t, body, command, home, kind)
}

func assertStdioDefinition(t *testing.T, body []byte, command, home string, kind Kind) {
	t.Helper()
	var definition stdioMCPDefinition
	if err := json.Unmarshal(body, &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Command != command || len(definition.Args) != 2 || definition.Args[0] != "mcp" || definition.Args[1] != "proxy" {
		t.Fatalf("stdio MCP definition = %#v", definition)
	}
	if definition.Env["LAZYMIND_HOME"] != home ||
		definition.Env["LAZYMIND_AGENT_PROVIDER"] != string(kind) ||
		definition.Env["LAZYMIND_AGENT_HOST_ID"] != "host-1" || len(definition.Env) != 3 {
		t.Fatalf("stdio MCP env = %#v", definition.Env)
	}
	if _, exists := definition.Env["LAZYMIND_ACCESS_TOKEN"]; exists {
		t.Fatal("setup must never expose a LazyMind access token")
	}
}

func jsonQuote(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
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
