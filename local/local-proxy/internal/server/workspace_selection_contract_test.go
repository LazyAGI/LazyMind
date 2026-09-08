package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

const (
	workspaceSelectPath    = "/_local/workspaces:select"
	workspaceAuthorizePath = "/_local/workspaces:authorize"
)

func workspaceContractConfig() config.Config {
	return config.Config{
		Listen: config.ListenConfig{Host: "127.0.0.1", Port: 5024},
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth.local",
		},
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:8090"},
		},
	}
}

func workspaceContractRequest(method, path, body, remoteAddr, origin string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	return req
}

func requireWorkspaceContractError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), status)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload["code"] != code {
		t.Fatalf("error code=%#v, want %q", payload["code"], code)
	}
}

func TestWorkspaceSelectionEndpointOnlyAllowsPost(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workspaceContractRequest(
		http.MethodGet, workspaceSelectPath, "", "127.0.0.1:50000", "http://localhost:8090",
	))
	requireWorkspaceContractError(t, response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
}

func TestWorkspaceSelectionRejectsNonLoopbackCaller(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workspaceContractRequest(
		http.MethodPost, workspaceSelectPath, `{}`, "192.168.1.20:50000", "http://localhost:8090",
	))
	requireWorkspaceContractError(t, response, http.StatusForbidden, "LOCAL_WORKSPACE_SELECTION_FORBIDDEN")
}

func TestWorkspaceSelectionRejectsCrossSiteOrigin(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workspaceContractRequest(
		http.MethodPost, workspaceSelectPath, `{}`, "127.0.0.1:50000", "https://attacker.example",
	))
	requireWorkspaceContractError(t, response, http.StatusForbidden, "LOCAL_WORKSPACE_SELECTION_FORBIDDEN")
}

func TestWorkspaceSelectionRequiresAnExplicitAllowedOrigin(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workspaceContractRequest(
		http.MethodPost, workspaceSelectPath, `{}`, "127.0.0.1:50000", "",
	))
	requireWorkspaceContractError(t, response, http.StatusForbidden, "LOCAL_WORKSPACE_SELECTION_FORBIDDEN")
}

func TestWorkspaceSelectionRejectsForwardedNonLoopbackCaller(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	request := workspaceContractRequest(
		http.MethodPost, workspaceSelectPath, `{}`, "127.0.0.1:50000", "http://localhost:8090",
	)
	request.Header.Set("X-Forwarded-For", "192.168.1.20")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	requireWorkspaceContractError(t, response, http.StatusForbidden, "LOCAL_WORKSPACE_SELECTION_FORBIDDEN")
}

func TestWorkspaceSelectionStaysDisabledForLANProfile(t *testing.T) {
	cfg := workspaceContractConfig()
	cfg.Listen.Host = "0.0.0.0"
	cfg.Auth.AutoLoginAllowLAN = true
	handler := NewHandler(cfg)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workspaceContractRequest(
		http.MethodPost, workspaceSelectPath, `{}`, "127.0.0.1:50000", "http://localhost:8090",
	))
	requireWorkspaceContractError(t, response, http.StatusForbidden, "LOCAL_WORKSPACE_MODE_FORBIDDEN")
}

func TestWorkspaceAuthorizationRejectsRendererSuppliedPath(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workspaceContractRequest(
		http.MethodPost,
		workspaceAuthorizePath,
		`{"path":"/Users/alice/Documents"}`,
		"127.0.0.1:50000",
		"http://localhost:8090",
	))
	requireWorkspaceContractError(t, response, http.StatusBadRequest, "LOCAL_WORKSPACE_SELECTION_INVALID")
}

func TestWorkspaceAuthorizationRejectsMissingOrTamperedSelectionToken(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	for _, body := range []string{
		`{}`,
		`{"selection_token":"tampered-token"}`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, workspaceContractRequest(
			http.MethodPost,
			workspaceAuthorizePath,
			body,
			"127.0.0.1:50000",
			"http://localhost:8090",
		))
		requireWorkspaceContractError(t, response, http.StatusBadRequest, "LOCAL_WORKSPACE_SELECTION_INVALID")
	}
}

func TestWorkspaceSelectionStoreDeclaresFiveMinuteSingleUseCandidates(t *testing.T) {
	paths, err := filepath.Glob("workspace*.go")
	if err != nil {
		t.Fatalf("find workspace implementation: %v", err)
	}
	var source strings.Builder
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		source.Write(body)
	}
	implementation := source.String()
	if !regexp.MustCompile(`5\s*\*\s*time\.Minute`).MatchString(implementation) {
		t.Fatal("workspace candidate token TTL must be declared as five minutes")
	}
	if !strings.Contains(implementation, "delete(") && !strings.Contains(implementation, ".Delete(") {
		t.Fatal("workspace candidate consumption must remove the token from the store")
	}
}

func TestWorkspaceSelectionErrorsDoNotLeakCandidatePath(t *testing.T) {
	handler := NewHandler(workspaceContractConfig())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, workspaceContractRequest(
		http.MethodPost,
		workspaceAuthorizePath,
		`{"selection_token":"tampered-/Users/alice/Documents"}`,
		"127.0.0.1:50000",
		"http://localhost:8090",
	))
	requireWorkspaceContractError(t, response, http.StatusBadRequest, "LOCAL_WORKSPACE_SELECTION_INVALID")
	if strings.Contains(response.Body.String(), "/Users/alice/Documents") {
		t.Fatalf("workspace error leaked a local path: %s", response.Body.String())
	}
}

func workspaceTestClient(core http.Handler) *http.Client {
	return &http.Client{Transport: &roundTripper{handlers: map[string]http.Handler{
		"auth.local": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/authservice/auth/login":
				_, _ = w.Write([]byte(`{"access_token":"token","refresh_token":"refresh","role":"system-admin","expires_in":3600}`))
			case "/api/authservice/auth/me":
				_, _ = w.Write([]byte(`{"user_id":"u-1","username":"admin","role":"system-admin"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}),
		"core": core,
	}}}
}

func workspaceTestHandler(t *testing.T, core http.Handler) *workspaceHandler {
	t.Helper()
	cfg := workspaceContractConfig()
	cfg.Routes = []config.RouteConfig{{Name: "core-route", Prefix: "/api/core", Upstream: "http://core", Enabled: true}}
	t.Setenv("LAZYMIND_LOCAL_WORKSPACE_HOST_TOKEN", "native-caller")
	return newWorkspaceHandler(cfg, workspaceTestClient(core))
}

func TestWorkspacePickerCancellationDoesNotCreateCandidate(t *testing.T) {
	handler := workspaceTestHandler(t, http.NotFoundHandler())
	handler.pick = func(context.Context) (string, error) { return "", errWorkspacePickerCanceled }
	response := httptest.NewRecorder()
	handler.selectWorkspace(response, workspaceContractRequest(http.MethodPost, workspaceSelectPath, `{}`, "127.0.0.1:50000", "http://localhost:8090"))
	if response.Code != http.StatusOK || response.Body.String() != "{\"canceled\":true}\n" || len(handler.store.items) != 0 {
		t.Fatalf("cancel response=%d %s candidates=%d", response.Code, response.Body.String(), len(handler.store.items))
	}
}

func TestWorkspaceCandidateExpiresIsSingleUseAndOwnerBound(t *testing.T) {
	store := newWorkspaceCandidateStore()
	now := time.Date(2026, 9, 8, 6, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	token, err := store.put(workspaceCandidate{path: "/tmp/project", name: "project", userID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, state := store.consume(token, "u-2"); state != "invalid" {
		t.Fatalf("cross-user state=%q", state)
	}
	if _, state := store.consume(token, "u-1"); state != "invalid" {
		t.Fatalf("cross-user attempt did not consume token: %q", state)
	}
	token, err = store.put(workspaceCandidate{path: "/tmp/project", name: "project", userID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(workspaceCandidateTTL)
	if _, state := store.consume(token, "u-1"); state != "expired" {
		t.Fatalf("expiry state=%q", state)
	}
	token, err = store.put(workspaceCandidate{path: "/tmp/project", name: "project", userID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, state := store.consume(token, "u-1"); state != "" {
		t.Fatalf("first consume=%q", state)
	}
	if _, state := store.consume(token, "u-1"); state != "invalid" {
		t.Fatalf("replay state=%q", state)
	}
}

func TestWorkspaceAuthorizationRechecksDirectoryAndSanitizesCoreFailure(t *testing.T) {
	root, _, rootInfo, err := validateWorkspaceDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-LazyMind-Local-Workspace-Token") != "native-caller" || r.Header.Get("X-User-Id") != "u-1" {
			t.Fatalf("missing Core identity headers: %v", r.Header)
		}
		http.Error(w, "private core failure /Users/alice", http.StatusInternalServerError)
	})
	handler := workspaceTestHandler(t, core)
	token, err := handler.store.put(workspaceCandidate{path: root, name: filepath.Base(root), userID: "u-1", info: rootInfo})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.authorizeWorkspace(response, workspaceContractRequest(http.MethodPost, workspaceAuthorizePath,
		`{"selection_token":"`+token+`"}`, "127.0.0.1:50000", "http://localhost:8090"))
	requireWorkspaceContractError(t, response, http.StatusServiceUnavailable, "LOCAL_WORKSPACE_SELECTION_INVALID")
	if strings.Contains(response.Body.String(), "private core") || strings.Contains(response.Body.String(), "/Users/alice") {
		t.Fatalf("Core failure leaked: %s", response.Body.String())
	}

	removed, _, removedInfo, err := validateWorkspaceDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, err = handler.store.put(workspaceCandidate{path: removed, name: filepath.Base(removed), userID: "u-1", info: removedInfo})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.authorizeWorkspace(response, workspaceContractRequest(http.MethodPost, workspaceAuthorizePath,
		`{"selection_token":"`+token+`"}`, "127.0.0.1:50000", "http://localhost:8090"))
	requireWorkspaceContractError(t, response, http.StatusBadRequest, "LOCAL_WORKSPACE_PATH_INVALID")
}

func TestWorkspaceReauthorizationRequiresPickerAndExactStoredDirectory(t *testing.T) {
	root, _, _, err := validateWorkspaceDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"canonical_path":"` + root + `","display_name":"project"}}`))
	})
	handler := workspaceTestHandler(t, core)
	pickerCalls := 0
	handler.pick = func(context.Context) (string, error) { pickerCalls++; return root, nil }
	request := workspaceContractRequest(http.MethodPost, "/_local/workspaces:reauthorize",
		`{"workspace_id":"grant"}`, "127.0.0.1:50000", "http://localhost:8090")
	response := httptest.NewRecorder()
	handler.reauthorizeWorkspace(response, request)
	if response.Code != http.StatusOK || pickerCalls != 1 || !strings.Contains(response.Body.String(), "selection_token") {
		t.Fatalf("reauthorize=%d %s pickerCalls=%d", response.Code, response.Body.String(), pickerCalls)
	}

	handler = workspaceTestHandler(t, core)
	handler.pick = func(context.Context) (string, error) { return t.TempDir(), nil }
	request = workspaceContractRequest(http.MethodPost, "/_local/workspaces:reauthorize",
		`{"workspace_id":"grant"}`, "127.0.0.1:50000", "http://localhost:8090")
	response = httptest.NewRecorder()
	handler.reauthorizeWorkspace(response, request)
	requireWorkspaceContractError(t, response, http.StatusBadRequest, "LOCAL_WORKSPACE_PATH_INVALID")
}
