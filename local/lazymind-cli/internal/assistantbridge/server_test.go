package assistantbridge

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
)

type stubBridgeProber struct{ err error }

func (p stubBridgeProber) Probe(context.Context) (mcpbridge.ProbeResult, error) {
	return mcpbridge.ProbeResult{}, p.err
}

func newTestServer(t *testing.T, home string) *Server {
	t.Helper()
	t.Setenv("LAZYMIND_HOME", home)
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := executorpolicy.New(home)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New("127.0.0.1:0", bridge, store, policy)
	if err != nil {
		t.Fatal(err)
	}
	server.executorProbe = stubBridgeProber{}
	return server
}

func TestExecutableBindingCanBeSavedListedAndCleared(t *testing.T) {
	home := t.TempDir()
	server := newTestServer(t, home)
	executable := filepath.Join(home, "custom-codex")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err := os.WriteFile(executable, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	handler := server.routes()

	request := httptest.NewRequest(http.MethodPut, "/v1/bindings/cursor-desktop", bytes.NewReader(
		[]byte(`{"path":`+strconv.Quote(executable)+`}`),
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"configured":true`)) {
		t.Fatalf("set status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/bindings", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"cursor-desktop"`)) {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/bindings/cursor-desktop", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"configured":false`)) {
		t.Fatalf("clear status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestStatusesDoNotLaunchDesktopAgentCandidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("PATH", "")
	t.Setenv("LAZYMIND_HOME", filepath.Join(home, ".lazymind"))
	t.Setenv("LAZYMIND_CODEX_BIN", "")
	cursorHome := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(cursorHome, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "cursor-started")
	if err := os.WriteFile(filepath.Join(cursorHome, "cursor.cmd"), []byte("touch "+marker), 0o700); err != nil {
		t.Fatal(err)
	}
	desktop := filepath.Join(home, "Cursor.exe")
	if err := os.WriteFile(desktop, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agentexec.SetExecutableBinding(agentexec.CursorDesktop, desktop); err != nil {
		t.Fatal(err)
	}

	statuses, err := Statuses(context.Background(), &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	cursor := statuses["cursor"]
	if cursor.State != agentintegration.Ready {
		t.Fatalf("cursor status=%#v", statuses["cursor"])
	}
	if _, exists := statuses["raccoon"]; !exists {
		t.Fatal("Raccoon status is missing")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("status inspection launched Cursor: %v", err)
	}
	traeConfig := filepath.Join(home, ".config", "TRAE SOLO CN", "User", "mcp.json")
	if runtime.GOOS == "darwin" {
		traeConfig = filepath.Join(home, "Library", "Application Support", "TRAE SOLO CN", "User", "mcp.json")
	} else if runtime.GOOS == "windows" {
		traeConfig = filepath.Join(home, "AppData", "Roaming", "TRAE SOLO CN", "User", "mcp.json")
	}
	for _, path := range []string{
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".cursor", "mcp.json"),
		filepath.Join(home, ".workbuddy", "mcp.json"),
		filepath.Join(home, ".box-agent", "config", "mcp.json"),
		traeConfig,
		filepath.Join(home, ".dsh", "profiles", "web", "cordis.patch.yml"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("status inspection wrote Agent configuration %s: %v", path, err)
		}
	}
}

func TestInteractiveLoginActionsReturnBeforeExternalLoginCompletes(t *testing.T) {
	for _, agent := range []string{"cursor"} {
		t.Run(agent, func(t *testing.T) {
			server := newTestServer(t, t.TempDir())
			changes := server.policy.Changes()
			started := make(chan struct{})
			release := make(chan struct{})
			server.loginOverride = func(context.Context, string) error {
				close(started)
				<-release
				return nil
			}

			start := time.Now()
			status, err := server.agentAction(context.Background(), agent, "login")
			if err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("login action blocked for %s", elapsed)
			}
			if !strings.Contains(status.Message, "return to LazyMind and check again") {
				t.Fatalf("status=%#v", status)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("background login did not start")
			}
			close(release)
			select {
			case <-changes:
			case <-time.After(time.Second):
				t.Fatal("completed login did not trigger an executor recheck")
			}
		})
	}
}

func TestBrowserSessionCanBeSavedAndCleared(t *testing.T) {
	home := t.TempDir()
	server := newTestServer(t, home)
	handler := server.routes()
	body := []byte(`{"server_url":"http://127.0.0.1:8090","access_token":"access","refresh_token":"refresh"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/session", bytes.NewReader(body))
	request.Header.Set("Origin", "http://127.0.0.1:8090")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/session", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8090")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("credential was not cleared: %v", err)
	}
}

func TestBrowserSessionRejectsAnotherServerOrigin(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	request := httptest.NewRequest(http.MethodPost, "/v1/session", bytes.NewReader(
		[]byte(`{"server_url":"http://127.0.0.1:8091","access_token":"access","refresh_token":"refresh"}`),
	))
	request.Header.Set("Origin", "http://127.0.0.1:8090")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBrowserAgentRoutesRejectAnotherHostPlatform(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	clientPlatform := "windows"
	if runtime.GOOS == "windows" {
		clientPlatform = "linux"
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	request.Header.Set("X-LazyMind-Client-Platform", clientPlatform)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "native") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBrowserPreflightAllowsClientPlatformHeader(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	request := httptest.NewRequest(http.MethodOptions, "/v1/agents", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8090")
	request.Header.Set("Access-Control-Request-Headers", clientPlatformHeader)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent ||
		!strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), clientPlatformHeader) {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}

func TestExecutorPolicyCanBeDisabledAndEnabled(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	handler := server.routes()

	request := httptest.NewRequest(http.MethodPost, "/v1/executors/codex/disable", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"enabled":false`)) {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/executors", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"codex":{"provider":"codex","enabled":false`)) {
		t.Fatalf("statuses status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/executors/codex/enable", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("enable status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExecutorStatusesProbePrerequisitesWithoutEnablingExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	home := t.TempDir()
	binary := filepath.Join(home, "agent-cli")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then exit 0; fi
if [ "$1" = "status" ]; then exit 0; fi
if [ "$1" = "login" ] && [ "$2" = "status" ]; then exit 0; fi
exit 1
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_CODEX_BIN", binary)
	t.Setenv("LAZYMIND_CURSOR_AGENT_BIN", binary)
	server := newTestServer(t, filepath.Join(home, "lazymind"))

	statuses, err := ExecutorStatuses(context.Background(), server.policy, server.workBuddyProbe)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "cursor"} {
		status := statuses[provider]
		if status.Enabled || !status.Installed || !status.Ready || status.UnavailableReason != "" {
			t.Fatalf("%s status=%#v", provider, status)
		}
	}
}

func TestExecutorStatusesExposeAssistantBridgeState(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	for _, test := range []struct {
		name  string
		err   error
		state executorpolicy.BridgeState
	}{
		{name: "ready", state: executorpolicy.BridgeReady},
		{name: "authentication", err: credentials.ErrAuthenticationRequired, state: executorpolicy.BridgeAuthenticationRequired},
		{name: "unavailable", err: errors.New("connection failed"), state: executorpolicy.BridgeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			statuses, err := ExecutorStatusesWithBridge(
				context.Background(), server.policy, stubBridgeProber{err: test.err},
				func(context.Context) (bool, bool, string) { return true, true, "" },
			)
			if err != nil {
				t.Fatal(err)
			}
			for provider, status := range statuses {
				if status.BridgeState != test.state {
					t.Errorf("%s bridge state=%q want %q", provider, status.BridgeState, test.state)
				}
			}
		})
	}
}
