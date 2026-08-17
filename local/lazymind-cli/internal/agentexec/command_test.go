package agentexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureConversationWorkspaceStaysInsideAgentRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOME", home)

	workspace, err := EnsureConversationWorkspace("conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "agent-workspaces", "conversation-1")
	if workspace != want {
		t.Fatalf("workspace = %q, want %q", workspace, want)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		t.Fatalf("workspace was not created: info=%v err=%v", info, err)
	}

	for _, invalid := range []string{"", ".", "..", filepath.Join("parent", "conversation-1")} {
		if _, err := EnsureConversationWorkspace(invalid); err == nil {
			t.Fatalf("conversation ID %q unexpectedly produced a workspace", invalid)
		}
	}
}

func TestLazyMindMCPConfigCarriesInvocationContext(t *testing.T) {
	body, err := LazyMindMCPConfig("/opt/lazymind", "/tmp/lazymind-home", "run-1", "conversation-1", "lease-1", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	server, ok := config.MCPServers["lazymind"]
	if !ok || server.Command != "/opt/lazymind" || len(server.Args) != 2 ||
		server.Args[0] != "mcp" || server.Args[1] != "proxy" ||
		server.Env["LAZYMIND_HOME"] != "/tmp/lazymind-home" ||
		server.Env["LAZYMIND_EXTERNAL_REF"] != "run-1" ||
		server.Env["LAZYMIND_EXTERNAL_LEASE"] != "lease-1" ||
		server.Env["LAZYMIND_EXTERNAL_HOST"] != "host-1" ||
		server.Env["LAZYMIND_CONVERSATION_ID"] != "conversation-1" {
		t.Fatalf("unexpected invocation MCP configuration: %#v", server)
	}
}

func TestSafeEnvironmentDropsUnrelatedServiceSecrets(t *testing.T) {
	t.Setenv("LAZYMIND_DATABASE_PASSWORD", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "provider-key")
	t.Setenv("SystemRoot", `C:\Windows`)
	environment := SafeEnvironment("LAZYMIND_EXTERNAL_LEASE=lease-1")
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	if values["LAZYMIND_DATABASE_PASSWORD"] != "" {
		t.Fatal("unrelated LazyMind secret was inherited by Agent process")
	}
	if values["OPENAI_API_KEY"] != "provider-key" || values["LAZYMIND_EXTERNAL_LEASE"] != "lease-1" {
		t.Fatalf("required Agent environment was lost: %#v", values)
	}
	if values["SystemRoot"] != `C:\Windows` {
		t.Fatalf("required Windows process environment was lost: %#v", values)
	}
}
