package assistantbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowconnector"
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
			if provider == workflowhost.DSHProvider {
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

func TestDSHHeartbeatKeepsLocalReadinessDuringCoreAuthorizationBackoff(t *testing.T) {
	var calls atomic.Int64
	var coreReady atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if !coreReady.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","error":"session expired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"actions":[]}`))
	}))
	defer upstream.Close()
	home := t.TempDir()
	server := newTestServer(t, home)
	if err := server.store.Save(credentials.Credentials{ServerURL: upstream.URL, AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600}); err != nil {
		t.Fatal(err)
	}
	account, err := server.store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := workflowhost.Ensure(home, t.TempDir(), account)
	if err != nil {
		t.Fatal(err)
	}
	pairPath := filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")
	configPath, err := connectorconfig.Save(connectorconfig.Config{
		Home: home, HostID: "host", Provider: pair.Provider, Profile: pair.Profile, PairingFile: pairPath, Controlled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := connectorconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	identity := workflowhost.DSHRuntimeIdentity{
		Dispatcher: "00000000-0000-0000-0000-000000000001", Process: "00000000-0000-0000-0000-000000000002",
		ConfigRevision: config.Revision(), PairingRevision: pair.RuntimeRevision,
	}
	lockPath := pairPath + ".runtime.lock"
	setLock := func(identity workflowhost.DSHRuntimeIdentity) {
		t.Helper()
		body := fmt.Sprintf(`{"pid":%d,"instanceId":%q,"processInstance":%q}`, os.Getpid(), identity.Dispatcher, identity.Process)
		if err := os.WriteFile(lockPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setLock(identity)
	routes := server.routes()
	started := strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10)
	request := func(method, path string, identity workflowhost.DSHRuntimeIdentity) int {
		r := httptest.NewRequest(method, "/v1/workflow-host/"+path, nil)
		r.Header.Set("Authorization", "Bearer "+pair.Token)
		r.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
		r.Header.Set("X-LazyMind-DSH-Dispatcher", identity.Dispatcher)
		r.Header.Set("X-LazyMind-DSH-Process", identity.Process)
		r.Header.Set("X-LazyMind-DSH-Config-Revision", identity.ConfigRevision)
		r.Header.Set("X-LazyMind-DSH-Pairing-Revision", identity.PairingRevision)
		r.Header.Set("X-LazyMind-DSH-Process-Started-At", started)
		r.Header.Set("X-LazyMind-DSH-Build", "0.1.6+0000000000000001")
		r.Header.Set("X-LazyMind-DSH-Client-Build", "0.1.6+0000000000000002")
		r.Header.Set("X-LazyMind-DSH-Panel", "bound")
		if method == http.MethodPost {
			r.Header.Set("X-LazyMind-MCP-Ready", "1")
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		return w.Code
	}
	expect := func(method, path string, identity workflowhost.DSHRuntimeIdentity, want int) {
		t.Helper()
		if status := request(method, path, identity); status != want {
			t.Fatalf("%s %s returned %d, want %d", method, path, status, want)
		}
	}
	read := func(issue string) workflowhost.RuntimeStatus {
		t.Helper()
		status, err := workflowhost.ReadRuntimeStatus(home, pair)
		if err != nil || !status.MCPReady || status.Build != "0.1.6+0000000000000001" ||
			status.ClientBuild != "0.1.6+0000000000000002" || status.Panel != "bound" || status.ConnectionIssue(pair) != issue {
			t.Fatalf("want local readiness and Core issue %q, got %+v %v", issue, status, err)
		}
		return status
	}

	// Core may hold this lock during token refresh. The local heartbeat can
	// still check the atomically published account snapshot.
	unlock, err := localfile.Lock(filepath.Join(home, "credentials.json.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	first := make(chan int, 1)
	go func() { first <- request(http.MethodPost, "heartbeat", identity) }()
	select {
	case status := <-first:
		if status != http.StatusOK || calls.Load() != 0 {
			t.Fatalf("first heartbeat returned %d and contacted Core %d times", status, calls.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local heartbeat waited for the credentials lock")
	}
	unlock()
	unlock = nil
	read(workflowconnector.ConnectionPending)
	expect(http.MethodGet, "actions", identity, http.StatusUnauthorized)
	observed := read("lazymind_auth_required")
	upstreamCount := calls.Load()

	// Advance both persisted clocks to a point past liveness expiry, while
	// the dispatcher would still be in its 30-second authorization backoff.
	observed.CheckedAt -= 18_000
	observed.ConnectionCheckedAt -= 18_000
	body, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pairPath+".runtime-status.json", body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowhost.ReadRuntimeStatus(home, pair); err == nil {
		t.Fatal("stale heartbeat still appeared live")
	}
	expect(http.MethodPost, "heartbeat", identity, http.StatusOK)
	during := read("lazymind_auth_required")
	if during.CheckedAt <= observed.CheckedAt || during.ConnectionCheckedAt != observed.ConnectionCheckedAt || calls.Load() != upstreamCount {
		t.Fatal("local heartbeat changed the Core observation or contacted Core")
	}

	coreReady.Store(true)
	expect(http.MethodGet, "actions", identity, http.StatusOK)
	recovered := read("")
	if recovered.ConnectionCheckedAt <= during.ConnectionCheckedAt || recovered.CheckedAt != during.CheckedAt {
		t.Fatal("Core recovery refreshed the local heartbeat")
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowhost.ReadRuntimeStatus(home, pair); err == nil {
		t.Fatal("stopped dispatcher retained readiness")
	}
	expect(http.MethodPost, "heartbeat", identity, http.StatusForbidden)

	newIdentity := identity
	newIdentity.Dispatcher = "00000000-0000-0000-0000-000000000003"
	newIdentity.Process = "00000000-0000-0000-0000-000000000004"
	setLock(newIdentity)
	expect(http.MethodPost, "heartbeat", newIdentity, http.StatusOK)
	expect(http.MethodGet, "actions", identity, http.StatusForbidden)
	read(workflowconnector.ConnectionPending)

	config.Overrides = map[string]string{"LAZYMIND_WEB_URL": "http://localhost:8090"}
	body, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowhost.ReadRuntimeStatus(home, pair); err == nil {
		t.Fatal("superseded configuration retained readiness")
	}
	expect(http.MethodPost, "heartbeat", newIdentity, http.StatusForbidden)
	newIdentity.ConfigRevision = config.Revision()
	expect(http.MethodPost, "heartbeat", newIdentity, http.StatusOK)
	if err := workflowhost.Disable(home, pair.ConnectorID); err != nil {
		t.Fatal(err)
	}
	expect(http.MethodPost, "heartbeat", newIdentity, http.StatusForbidden)
	if _, err := workflowhost.ReadRuntimeStatus(home, pair); err == nil {
		t.Fatal("revoked pairing retained readiness")
	}
	if _, err := workflowhost.Ensure(home, pair.Profile, account); err != nil {
		t.Fatal(err)
	}
	expect(http.MethodPost, "heartbeat", newIdentity, http.StatusForbidden)
}
