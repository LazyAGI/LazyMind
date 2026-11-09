package assistantbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestWorkflowBindUsesPairedProviderNotRequestBody(t *testing.T) {
	for _, provider := range []string{workflowhost.DSHProvider, "fake-agent", "workbuddy", "codex"} {
		t.Run(provider, func(t *testing.T) {
			var received map[string]string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/core/workflow-sessions/run-1/host-binding" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"control":{"protocol":"workflow.control.v1"}}`))
			}))
			defer upstream.Close()
			home := t.TempDir()
			server := newTestServer(t, home)
			if err := server.store.Save(credentials.Credentials{ServerURL: upstream.URL,
				AccessToken: "access", RefreshToken: "refresh", SavedAt: float64(time.Now().Unix()), ExpiresIn: 3600,
			}); err != nil {
				t.Fatal(err)
			}
			account, err := server.store.AccountScope()
			if err != nil {
				t.Fatal(err)
			}
			pair, err := workflowhost.EnsureForProvider(home, provider, filepath.Join(home, "profile"), account)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/workflow-host/bind", strings.NewReader(`{"run_id":"run-1","driver_session_id":"native-1","provider":"spoofed"}`))
			request.Header.Set("Authorization", "Bearer "+pair.Token)
			request.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
			response := httptest.NewRecorder()
			server.routes().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if received["provider"] != provider || received["connector_id"] != pair.ConnectorID || received["driver_session_id"] != "native-1" {
				t.Fatal("binding did not use trusted pairing identity")
			}
			wantCancellation := "none"
			if provider == workflowhost.DSHProvider || provider == "workbuddy" {
				wantCancellation = "session"
			}
			if received["cancellation"] != wantCancellation {
				t.Fatalf("unverified cancellation capability: %v", received)
			}
		})
	}
}

func TestWorkflowHostPairingFailureHasStableRepairCode(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	if err := server.store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:19000", AccessToken: "access", RefreshToken: "refresh", SavedAt: float64(time.Now().Unix()), ExpiresIn: 3600}); err != nil {
		t.Fatal(err)
	}
	scope, err := server.store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := workflowhost.EnsureForProvider(server.store.Directory(), "workbuddy", t.TempDir(), scope)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/workflow-host/actions", nil)
	request.Header.Set("Authorization", "Bearer deliberately-wrong")
	request.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	var body map[string]string
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 403 || body["code"] != "HOST_CREDENTIAL_MISMATCH" {
		t.Fatalf("status=%d response=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "deliberately-wrong") || strings.Contains(response.Body.String(), pair.Token) {
		t.Fatal("response disclosed credential")
	}
}

func TestWorkflowHostDistinguishesSignedOutFromUnreadableCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		status     int
	}{
		{"missing", "HOST_AUTH_REQUIRED", http.StatusUnauthorized},
		{"unreadable", "WORKFLOW_HOST_UNAVAILABLE", http.StatusBadGateway},
		{"corrupt", "WORKFLOW_HOST_UNAVAILABLE", http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			server := newTestServer(t, home)
			path := filepath.Join(home, "credentials.json")
			if tc.name == "unreadable" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if tc.name == "corrupt" {
				if err := os.WriteFile(path, []byte(`{"access_token":"SECRET",`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			response := httptest.NewRecorder()
			server.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/workflow-host/actions", nil))
			var body map[string]string
			json.Unmarshal(response.Body.Bytes(), &body)
			if response.Code != tc.status || body["code"] != tc.code {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "SECRET") {
				t.Fatal("credential disclosed")
			}
		})
	}
}

func TestDSHRuntimeRequiresAuthenticatedPollAndExplicitMCPReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, header, issue string
		upstreamStatus      int
		wrongToken, wantMCP bool
	}{
		{name: "ready", header: "1", upstreamStatus: 200, wantMCP: true},
		{name: "no MCP tools", header: "0", upstreamStatus: 200},
		{name: "legacy plugin", upstreamStatus: 200},
		{name: "core unavailable", header: "1", upstreamStatus: 503, wantMCP: true, issue: "lazymind_unavailable"},
		{name: "core rejects authentication", header: "1", upstreamStatus: 401, wantMCP: true, issue: "lazymind_auth_required"},
		{name: "binding error is not expired login", header: "1", upstreamStatus: 409, wantMCP: true, issue: "lazymind_unavailable"},
		{name: "unauthenticated", header: "1", upstreamStatus: 200, wrongToken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.upstreamStatus)
				if tc.upstreamStatus == 200 {
					// A Workflow waiting to bind is not a broken transport.
					_, _ = w.Write([]byte(`{"actions":[],"control":{"continuation":"binding_required"}}`))
				} else {
					_, _ = w.Write([]byte(`{"code":"BINDING_REQUIRED","error":"binding_required"}`))
				}
			}))
			defer upstream.Close()
			home := t.TempDir()
			server := newTestServer(t, home)
			if err := server.store.Save(credentials.Credentials{ServerURL: upstream.URL, AccessToken: "access", RefreshToken: "refresh", SavedAt: float64(time.Now().Unix()), ExpiresIn: 3600}); err != nil {
				t.Fatal(err)
			}
			account, _ := server.store.AccountScope()
			pair, err := workflowhost.Ensure(home, t.TempDir(), account)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/v1/workflow-host/actions", nil)
			token := pair.Token
			if tc.wrongToken {
				token = "wrong"
			}
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
			request.Header.Set("X-LazyMind-MCP-Ready", tc.header)
			response := httptest.NewRecorder()
			server.routes().ServeHTTP(response, request)
			status, err := workflowhost.ReadRuntimeStatus(home, pair)
			if tc.wrongToken {
				if response.Code != http.StatusForbidden || err == nil {
					t.Fatalf("unauthenticated poll recorded runtime readiness: response=%d status=%+v err=%v", response.Code, status, err)
				}
				return
			}
			if err != nil || status.MCPReady != tc.wantMCP || status.Issue != tc.issue {
				t.Fatalf("runtime status=%+v err=%v", status, err)
			}
		})
	}
}
