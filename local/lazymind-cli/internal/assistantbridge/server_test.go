package assistantbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
			defer close(release)
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
			body, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(body, []byte(`"login":{"state":"running"`)) {
				t.Fatalf("status=%s", body)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("background login did not start")
			}
			release <- struct{}{}
			select {
			case <-changes:
			case <-time.After(time.Second):
				t.Fatal("completed login did not trigger an executor recheck")
			}
		})
	}
}

func TestCodexLoginActionReportsRealAdapterCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	server := newLoginTestServer(t)
	t.Setenv("SHELL", "")
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", t.TempDir())
	binary := filepath.Join(t.TempDir(), "codex")
	authenticated := filepath.Join(t.TempDir(), "authenticated")
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
  "--version ") echo 'codex-cli 0.100.0' ;;
  "login ") printf authenticated > %q ;;
  "login status") test -f %q ;;
  *) exit 2 ;;
esac
`, authenticated, authenticated)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_CODEX_BIN", binary)
	before := readAgentLoginResponse(t, server, "codex", false)
	changes := server.policy.Changes()
	status, err := server.agentAction(context.Background(), "codex", "login")
	if err != nil {
		t.Fatal(err)
	}
	if status.Login == nil || status.State != before.State || status.Message != before.Message {
		t.Fatalf("login changed MCP status: before=%#v after=%#v", before, status)
	}
	// Version, login, and login-status each start a subprocess; allow scheduling time during the full suite.
	select {
	case <-changes:
	case <-time.After(10 * time.Second):
		t.Fatal("real adapter login did not complete and recheck executor status")
	}
	completed := readAgentLoginResponse(t, server, "codex", false)
	if completed.Login == nil || completed.Login.State != "succeeded" {
		t.Fatalf("real adapter login outcome=%+v", completed.Login)
	}
}

type agentLoginResponse struct {
	State   agentintegration.State `json:"state"`
	Message string                 `json:"message"`
	Login   *struct {
		State   string `json:"state"`
		Message string `json:"message"`
	} `json:"login"`
}

func readAgentLoginResponse(t *testing.T, server *Server, agent string, all bool) agentLoginResponse {
	t.Helper()
	path := "/v1/agents/" + agent
	if all {
		path = "/v1/agents"
	}
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var status agentLoginResponse
	if all {
		var result struct {
			Agents map[string]agentLoginResponse `json:"agents"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		status = result.Agents[agent]
	} else if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func newLoginTestServer(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PATH", "")
	t.Setenv("LAZYMIND_CODEX_BIN", "")
	return newTestServer(t, filepath.Join(home, ".lazymind"))
}

func waitLoginRecheck(t *testing.T, changes <-chan struct{}) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("login did not complete and recheck executor status")
	}
}

func TestBackgroundLoginOutcomesAppearInStatusWithoutChangingMCPState(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		state   string
		message string
	}{
		{name: "success", state: "succeeded"},
		{name: "failure", err: errors.New("browser login could not start"), state: "failed", message: "browser login could not start"},
		{name: "long failure", err: errors.New(strings.Repeat("登录失败", 1024)), state: "failed", message: "登录失败"},
		{name: "timeout", err: context.DeadlineExceeded, state: "failed", message: "timed out"},
		{name: "cancelled", err: context.Canceled, state: "cancelled", message: "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newLoginTestServer(t)
			before := readAgentLoginResponse(t, server, "codex", false)
			changes := server.policy.Changes()
			release := make(chan struct{})
			server.startAgentLogin("codex", func(context.Context) error {
				<-release
				return test.err
			})
			for _, all := range []bool{false, true} {
				status := readAgentLoginResponse(t, server, "codex", all)
				if status.Login == nil || status.Login.State != "running" {
					close(release)
					waitLoginRecheck(t, changes)
					t.Fatalf("all=%v running status=%#v", all, status)
				}
			}
			close(release)
			waitLoginRecheck(t, changes)
			for _, all := range []bool{false, true} {
				status := readAgentLoginResponse(t, server, "codex", all)
				if status.Login == nil || status.Login.State != test.state || !strings.Contains(status.Login.Message, test.message) {
					t.Fatalf("all=%v terminal status=%#v, login=%+v", all, status, status.Login)
				}
				if len([]rune(status.Login.Message)) > 2048 {
					t.Fatal("login failure message was not bounded")
				}
				if status.State != before.State || status.Message != before.Message {
					t.Fatalf("login changed MCP status: before=%#v after=%#v", before, status)
				}
			}
		})
	}
}

func TestSupersededLoginCannotReplaceLatestOutcome(t *testing.T) {
	server := newLoginTestServer(t)
	oldCancelled := make(chan struct{})
	oldRelease := make(chan struct{})
	defer close(oldRelease)
	server.startAgentLogin("codex", func(ctx context.Context) error {
		<-ctx.Done()
		close(oldCancelled)
		<-oldRelease
		return errors.New("stale failure")
	})
	changes := server.policy.Changes()
	server.startAgentLogin("codex", func(context.Context) error { return nil })
	waitLoginRecheck(t, changes)
	select {
	case <-oldCancelled:
	case <-time.After(time.Second):
		t.Fatal("superseded login was not cancelled")
	}
	status := readAgentLoginResponse(t, server, "codex", false)
	if status.Login == nil || status.Login.State != "succeeded" {
		t.Fatalf("latest status=%#v login=%+v", status, status.Login)
	}
	changes = server.policy.Changes()
	oldRelease <- struct{}{}
	waitLoginRecheck(t, changes)
	status = readAgentLoginResponse(t, server, "codex", false)
	if status.Login == nil || status.Login.State != "succeeded" {
		t.Fatalf("stale login replaced latest outcome: %#v login=%+v", status, status.Login)
	}
}

func TestExecutableBindingChangesCancelAndClearOnlyRelatedLogin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			server := newLoginTestServer(t)
			binary := filepath.Join(t.TempDir(), "codex")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			changes := server.policy.Changes()
			server.startAgentLogin("cursor", func(context.Context) error { return nil })
			waitLoginRecheck(t, changes)
			cancelled := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			server.startAgentLogin("codex", func(ctx context.Context) error {
				<-ctx.Done()
				close(cancelled)
				<-release
				return ctx.Err()
			})
			defer server.cancelAgentLogins()
			response := httptest.NewRecorder()
			request := httptest.NewRequest(method, "/v1/bindings/codex-cli", strings.NewReader(`{"path":`+strconv.Quote(binary)+`}`))
			server.routes().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("binding status=%d body=%s", response.Code, response.Body.String())
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("binding change did not cancel the previous login")
			}
			changes = server.policy.Changes()
			release <- struct{}{}
			waitLoginRecheck(t, changes)
			if status := readAgentLoginResponse(t, server, "codex", false); status.Login != nil {
				t.Fatalf("binding change retained old login: %+v", status.Login)
			}
			if status := readAgentLoginResponse(t, server, "cursor", false); status.Login == nil || status.Login.State != "succeeded" {
				t.Fatalf("binding change cleared unrelated login: %+v", status.Login)
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

func TestBrowserAssistantRoutesRejectAnotherHostPlatform(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	clientPlatform := "windows"
	if runtime.GOOS == "windows" {
		clientPlatform = "linux"
	}
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/session"},
		{http.MethodGet, "/v1/agents"},
		{http.MethodGet, "/v1/executors"},
		{http.MethodGet, "/v1/bindings"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set(clientPlatformHeader, clientPlatform)
			response := httptest.NewRecorder()
			server.routes().ServeHTTP(response, request)
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var payload map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["code"] != platformMismatchCode || payload["client_platform"] != clientPlatform ||
				payload["bridge_platform"] != runtime.GOOS || !strings.Contains(payload["error"], "native") {
				t.Fatalf("payload=%#v", payload)
			}
		})
	}
}

func TestBrowserHealthReportsBridgeIdentityAcrossPlatforms(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	clientPlatform := "windows"
	if runtime.GOOS == "windows" {
		clientPlatform = "linux"
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	request.Header.Set(clientPlatformHeader, clientPlatform)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["platform"] != runtime.GOOS || strings.TrimSpace(fmt.Sprint(payload["executable"])) == "" {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestValidateBridgeIdentityRequiresMatchingPlatformAndExecutable(t *testing.T) {
	root := t.TempDir()
	expected := filepath.Join(root, "lazymind")
	foreign := filepath.Join(root, "other-lazymind")
	if runtime.GOOS == "windows" {
		expected += ".exe"
		foreign += ".exe"
	}
	for _, path := range []string{expected, foreign} {
		if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateBridgeIdentity(map[string]any{
		"platform": runtime.GOOS, "executable": expected,
	}, expected); err != nil {
		t.Fatalf("matching identity: %v", err)
	}
	if err := validateBridgeIdentity(map[string]any{
		"platform": runtime.GOOS, "executable": foreign,
	}, expected); err == nil || !strings.Contains(err.Error(), "another LazyMind executable") {
		t.Fatalf("foreign executable error=%v", err)
	}
	otherPlatform := "windows"
	if runtime.GOOS == "windows" {
		otherPlatform = "linux"
	}
	if err := validateBridgeIdentity(map[string]any{
		"platform": otherPlatform, "executable": expected,
	}, expected); err == nil || !strings.Contains(err.Error(), "already running on") {
		t.Fatalf("platform mismatch error=%v", err)
	}
	if err := validateBridgeIdentity(map[string]any{}, expected); err == nil ||
		!strings.Contains(err.Error(), "does not report its platform") {
		t.Fatalf("missing identity error=%v", err)
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

	statuses, err := ExecutorStatuses(server.policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "cursor"} {
		status := statuses[provider]
		if status.Enabled || status.Installed == nil || !*status.Installed || status.Ready == nil || !*status.Ready || status.UnavailableReason != "" {
			t.Fatalf("%s status=%#v", provider, status)
		}
	}
	body, err := json.Marshal(statuses)
	if err != nil {
		t.Fatal(err)
	}
	var details map[string]struct {
		CLIPath   string `json:"cli_path"`
		CLISource string `json:"cli_source"`
	}
	if err := json.Unmarshal(body, &details); err != nil {
		t.Fatal(err)
	}
	resolvedBinary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if details["codex"].CLIPath != resolvedBinary || details["codex"].CLISource != "explicit" {
		t.Fatalf("Codex runtime details=%+v", details["codex"])
	}
	if details["cursor"].CLIPath != "" || details["cursor"].CLISource != "" {
		t.Fatalf("unexpected Cursor runtime details=%+v", details["cursor"])
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
