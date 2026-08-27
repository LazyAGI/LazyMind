package agentexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestExecutableBindingsPersistAndClear(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOME", home)
	name := "codex-custom"
	body := "#!/bin/sh\necho 1.0.0\n"
	if runtime.GOOS == "windows" {
		name += ".cmd"
		body = "@echo off\r\necho 1.0.0\r\n"
	}
	executable := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(executable, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	resolved, err := SetExecutableBinding(CodexCLI, executable)
	if err != nil {
		t.Fatal(err)
	}
	if !SameExecutable(resolved, executable) {
		t.Fatalf("resolved=%q want=%q", resolved, executable)
	}
	bindings, err := ExecutableBindings()
	if err != nil || !SameExecutable(bindings[CodexCLI], executable) {
		t.Fatalf("bindings=%#v err=%v", bindings, err)
	}
	if info, err := os.Stat(filepath.Join(home, "agent-bindings.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("binding file info=%v err=%v", info, err)
	}
	if err := ClearExecutableBinding(CodexCLI); err != nil {
		t.Fatal(err)
	}
	if _, configured, err := ExecutableBinding(CodexCLI); err != nil || configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
	if _, err := SetExecutableBinding("unknown", executable); err == nil {
		t.Fatal("unsupported binding target was accepted")
	}
}

func TestFindBoundPrefersExplicitThenEnvironmentThenSavedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOME", home)
	directory := t.TempDir()
	write := func(name string) string {
		body := "#!/bin/sh\necho 1.0.0\n"
		if runtime.GOOS == "windows" {
			name += ".cmd"
			body = "@echo off\r\necho 1.0.0\r\n"
		}
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	saved := write("saved")
	environment := write("environment")
	explicit := write("explicit")
	if _, err := SetExecutableBinding(CursorCLI, saved); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_CURSOR_BIN", environment)

	resolved, err := FindBoundExecutable(explicit, "TEST_CURSOR_BIN", CursorCLI, nil, nil)
	if err != nil || !SameExecutable(resolved, explicit) {
		t.Fatalf("explicit resolved=%q err=%v", resolved, err)
	}
	resolved, err = FindBoundExecutable("", "TEST_CURSOR_BIN", CursorCLI, nil, nil)
	if err != nil || !SameExecutable(resolved, environment) {
		t.Fatalf("environment resolved=%q err=%v", resolved, err)
	}
	t.Setenv("TEST_CURSOR_BIN", "")
	resolved, err = FindBoundExecutable("", "TEST_CURSOR_BIN", CursorCLI, nil, nil)
	if err != nil || !SameExecutable(resolved, saved) {
		t.Fatalf("saved resolved=%q err=%v", resolved, err)
	}
}
