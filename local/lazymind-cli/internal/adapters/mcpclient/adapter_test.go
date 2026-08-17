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
	assertStdioDefinition(t, encoded, adapter.self, adapter.home)
	assertMCPFile(t, guide.Configuration, adapter.self, adapter.home)
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
	assertMCPFile(t, guide.Configuration, adapter.self, adapter.home)
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
	assertMCPFile(t, guide.Configuration, adapter.self, adapter.home)
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

func testAdapter(kind Kind) *Adapter {
	return &Adapter{
		kind: kind, definition: definition{agent: string(kind)},
		binary: "/Applications/Agent.app/Contents/MacOS/agent",
		self:   "/Applications/LazyMind.app/Contents/Resources/runtime/bin/lazymind",
		home:   os.Getenv("LAZYMIND_HOME"),
	}
}

func assertMCPFile(t *testing.T, value, command, home string) {
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
	assertStdioDefinition(t, body, command, home)
}

func assertStdioDefinition(t *testing.T, body []byte, command, home string) {
	t.Helper()
	var definition stdioMCPDefinition
	if err := json.Unmarshal(body, &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Command != command || len(definition.Args) != 2 || definition.Args[0] != "mcp" || definition.Args[1] != "proxy" {
		t.Fatalf("stdio MCP definition = %#v", definition)
	}
	if definition.Env["LAZYMIND_HOME"] != home || len(definition.Env) != 1 {
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
