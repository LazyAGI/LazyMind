package assistantbridge

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
)

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
	if runtime.GOOS == "windows" {
		desktop := filepath.Join(home, "Cursor.exe")
		if err := os.WriteFile(desktop, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := agentexec.SetExecutableBinding(agentexec.CursorDesktop, desktop); err != nil {
			t.Fatal(err)
		}
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
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"codex":{"provider":"codex","enabled":false}`)) {
		t.Fatalf("statuses status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/executors/codex/enable", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("enable status=%d body=%s", response.Code, response.Body.String())
	}
}
