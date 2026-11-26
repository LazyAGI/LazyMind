package assistantbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestDSHExecutionRequiresIndependentPermissionAndVerifiedProfile(t *testing.T) {
	var claimReady bool
	var disableDuringClaim bool
	var reason, event string
	var calls int
	home := t.TempDir()
	server := newTestServer(t, home)
	var pair workflowhost.Pairing
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["host_id"] != pair.ConnectorID {
			t.Errorf("expected paired host on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/core/external-chat/hosts/deepseek-harness/claim":
			claimReady = body["ready"] == true
			reason, _ = body["unavailable_reason"].(string)
			if claimReady {
				if disableDuringClaim {
					if _, err := server.policy.SetEnabled(pair.Provider, false); err != nil {
						t.Error(err)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"run": map[string]string{
					"host_id": pair.ConnectorID, "run_id": "run", "provider": pair.Provider, "lease_token": "lease", "prompt": "test prompt",
				}})
			} else {
				_, _ = w.Write([]byte(`{"run":null}`))
			}
		case "/api/core/external-chat/runs/run:event":
			event, _ = body["type"].(string)
			_, _ = w.Write([]byte(`{"sequence":1}`))
		default:
			t.Errorf("unexpected relay %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	if err := server.store.Save(credentials.Credentials{ServerURL: upstream.URL, AccessToken: "test", RefreshToken: "refresh", SavedAt: float64(time.Now().Unix()), ExpiresIn: 3600}); err != nil {
		t.Fatal(err)
	}
	account, err := server.store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "desktop")
	pair, err = workflowhost.Ensure(home, profile, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connectorconfig.Save(connectorconfig.Config{Version: 1, Home: home, HostID: "machine", Provider: pair.Provider, Profile: profile, Controlled: true, PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")}); err != nil {
		t.Fatal(err)
	}
	post := func(path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/workflow-host/chat/"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
		resp := httptest.NewRecorder()
		server.routes().ServeHTTP(resp, req)
		return resp
	}
	const ready = `{"ready":true,"host_id":"spoof","enabled":true}`
	if got := post("claim", ready, "wrong"); got.Code != 403 || calls != 0 {
		t.Fatalf("invalid pairing: %d, calls=%d", got.Code, calls)
	}
	if got := post("claim", ready, pair.Token); got.Code != 200 || claimReady || !strings.Contains(got.Body.String(), `"run":null`) || reason != dshExecutionDisabled {
		t.Fatal("MCP pairing gave execution permission")
	}
	if _, err := server.policy.SetEnabled(pair.Provider, true); err != nil {
		t.Fatal(err)
	}
	if got := post("claim", `{"ready":false,"unavailable_reason":"DSH default model unavailable"}`, pair.Token); got.Code != 200 || claimReady || reason != "DSH default model unavailable" {
		t.Fatal("missing model was advertised as ready")
	}
	if got := post("claim", ready, pair.Token); got.Code != 200 || !claimReady || !strings.Contains(got.Body.String(), "test prompt") {
		t.Fatal("enabled, ready profile could not claim")
	}
	if got := post("run/event", `{"lease_token":"lease","event_id":"thread","type":"thread_started","provider_thread_id":"native","host_id":"spoof"}`, pair.Token); got.Code != 200 || event != "thread_started" {
		t.Fatal("could not bind the managed session")
	}
	if _, err := server.policy.SetEnabled(pair.Provider, false); err != nil {
		t.Fatal(err)
	}
	if got := post("run/heartbeat", `{"lease_token":"lease"}`, pair.Token); got.Code != 200 || !strings.Contains(got.Body.String(), `"stop_requested":true`) || event != "failed" {
		t.Fatal("disabled active execution was not stopped")
	}
	if _, err := server.policy.SetEnabled(pair.Provider, true); err != nil {
		t.Fatal(err)
	}
	disableDuringClaim = true
	event = ""
	if got := post("claim", ready, pair.Token); got.Code != 200 || !strings.Contains(got.Body.String(), `"run":null`) || event != "failed" {
		t.Fatal("prompt escaped after permission was withdrawn during the long poll")
	}
}
