package editor

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
	adapter := &Adapter{
		definition: definition{agent: string(Cursor)},
		self:       "/Applications/LazyMind.app/Contents/Resources/runtime/bin/lazymind",
		home:       os.Getenv("LAZYMIND_HOME"),
	}
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

	var fallback cursorMCPFile
	if err := json.Unmarshal([]byte(guide.Configuration), &fallback); err != nil {
		t.Fatal(err)
	}
	definition, exists := fallback.MCPServers[serverName]
	if !exists {
		t.Fatalf("Cursor fallback has no %q server", serverName)
	}
	fallbackBody, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	assertStdioDefinition(t, fallbackBody, adapter.self, adapter.home)
	if !strings.HasSuffix(guide.ConfigPath, filepath.Join(".cursor", "mcp.json")) {
		t.Fatalf("Cursor fallback path = %q", guide.ConfigPath)
	}
}

func TestWorkBuddyGuideUsesMCPFileConfiguration(t *testing.T) {
	t.Setenv("LAZYMIND_HOME", filepath.Join(t.TempDir(), "LazyMind Home"))
	adapter := &Adapter{
		definition: definition{agent: string(WorkBuddy)},
		binary:     "/Applications/CodeBuddy CN.app/Contents/Resources/app/bin/code",
		self:       "/Applications/LazyMind.app/Contents/Resources/runtime/bin/lazymind",
		home:       os.Getenv("LAZYMIND_HOME"),
	}
	guide, err := adapter.setupGuide()
	if err != nil {
		t.Fatal(err)
	}
	if guide.Method != SetupConfigFile || guide.URL != "" {
		t.Fatalf("WorkBuddy setup guide = %#v", guide)
	}
	var configuration cursorMCPFile
	if err := json.Unmarshal([]byte(guide.Configuration), &configuration); err != nil {
		t.Fatal(err)
	}
	definition, exists := configuration.MCPServers[serverName]
	if !exists {
		t.Fatalf("WorkBuddy configuration has no %q server", serverName)
	}
	body, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	assertStdioDefinition(t, body, adapter.self, adapter.home)
	if !strings.HasSuffix(guide.ConfigPath, filepath.Join(".codebuddy", "mcp.json")) {
		t.Fatalf("WorkBuddy config path = %q", guide.ConfigPath)
	}
}

func assertStdioDefinition(t *testing.T, body []byte, command, home string) {
	t.Helper()
	var definition stdioMCPDefinition
	if err := json.Unmarshal(body, &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Type != "stdio" || definition.Command != command || len(definition.Args) != 2 || definition.Args[0] != "mcp" || definition.Args[1] != "proxy" {
		t.Fatalf("stdio MCP definition = %#v", definition)
	}
	if definition.Env["LAZYMIND_HOME"] != home || len(definition.Env) != 1 {
		t.Fatalf("stdio MCP env = %#v", definition.Env)
	}
	if _, exists := definition.Env["LAZYMIND_ACCESS_TOKEN"]; exists {
		t.Fatal("setup must never expose a LazyMind access token")
	}
}
