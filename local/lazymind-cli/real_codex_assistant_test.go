package agentconnector_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/executorpolicy"
)

// Opt-in: real desktop CLI, an authenticated Core, and one small model response.
// All writes (plugins, session data, credentials refresh) stay in disposable homes.
func TestRealCodexAssistantRecovery(t *testing.T) {
	if os.Getenv("LAZYMIND_REAL_CODEX_RECOVERY") != "1" {
		t.Skip("requires an installed, authenticated Codex desktop and live LazyMind Core")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("macOS desktop recovery scenario")
	}
	binary := os.Getenv("LAZYMIND_REAL_CONNECTOR_BIN")
	actualCLI := os.Getenv("LAZYMIND_REAL_CODEX_BIN")
	credentialsFile := os.Getenv("LAZYMIND_REAL_CREDENTIALS_FILE")
	authFile := os.Getenv("LAZYMIND_REAL_CODEX_AUTH_FILE")
	if binary == "" || actualCLI == "" || credentialsFile == "" || authFile == "" {
		t.Fatal("set the real connector, desktop CLI, LazyMind credentials and Codex auth file paths")
	}
	root := t.TempDir()
	lmHome, codexHome := filepath.Join(root, "lazymind"), filepath.Join(root, "codex")
	for _, pair := range [][2]string{{credentialsFile, filepath.Join(lmHome, "credentials.json")}, {authFile, filepath.Join(codexHome, "auth.json")}} {
		body, err := os.ReadFile(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(pair[1]), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pair[1], body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", root)
	t.Setenv("LAZYMIND_HOME", lmHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("LAZYMIND_CODEX_BIN", "")
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", "/Applications")
	t.Setenv("SHELL", "")
	broken := filepath.Join(root, "broken-npm")
	if err := os.MkdirAll(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "codex"), []byte("#!/bin/sh\nexec /missing/codex-native-binary \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", broken+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	connector := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = os.Environ()
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("connector %v: %v\n%s", args, err, output)
		}
		return output
	}
	var statuses struct {
		Executors map[string]executorpolicy.Status `json:"executors"`
	}
	if err := json.Unmarshal(connector("internal", "executor", "all", "status"), &statuses); err != nil {
		t.Fatal(err)
	}
	policy := statuses.Executors["codex"]
	expected, _ := filepath.EvalSymlinks(actualCLI)
	if policy.CLIPath != expected || policy.CLISource != "desktop" || policy.Ready == nil || !*policy.Ready {
		t.Fatalf("real runtime probe: %+v", policy)
	}
	t.Logf("selected working desktop CLI %s despite broken PATH candidate", policy.CLIPath)
	action := func(name string) agentintegration.Status {
		t.Helper()
		var s agentintegration.Status
		if err := json.Unmarshal(connector("internal", "agent", "codex", name), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := action("connect"); s.State != agentintegration.Enabled {
		t.Fatalf("real plugin installation: %+v", s)
	}
	if s := action("status"); s.State != agentintegration.Enabled {
		t.Fatalf("installed plugin status: %+v", s)
	}
	t.Log("real Codex plugin installation and status passed in disposable profiles")
	command := exec.CommandContext(ctx, binary, "mcp", "proxy")
	command.Env = os.Environ()
	var proxyStderr bytes.Buffer
	command.Stderr = &proxyStderr
	session, err := mcp.NewClient(&mcp.Implementation{Name: "codex-recovery-real-test", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("real MCP initialization: %v; connector diagnostic: %s", err, proxyStderr.String())
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name == "workflow.list" {
			found = true
		}
	}
	if !found {
		t.Fatal("real Core did not expose workflow.list")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.list", Arguments: map[string]any{"page": map[string]any{"page_size": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("real workflow.list returned an MCP error")
	}
	t.Log("authenticated MCP handshake, tool discovery and real workflow.list passed")
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if s := action("disconnect"); s.State != agentintegration.Ready {
		t.Fatalf("real plugin disconnect: %+v", s)
	}
	cmd := exec.CommandContext(ctx, actualCLI, "exec", "--json", "--skip-git-repo-check", "--ignore-user-config", "-C", root, "-")
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader("Reply with exactly LAZYMIND_CODEX_RECOVERY_OK. Do not use tools.")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("real Codex response failed: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "LAZYMIND_CODEX_RECOVERY_OK") || !strings.Contains(stdout.String(), "turn.completed") {
		t.Fatal("real Codex did not complete the expected response")
	}
	t.Log("real authenticated Codex model response passed")
}
