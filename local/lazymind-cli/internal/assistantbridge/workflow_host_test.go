package assistantbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestWorkflowBindUsesPairedProviderNotRequestBody(t *testing.T) {
	for _, provider := range []string{workflowhost.DSHProvider, "fake-agent"} {
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
		})
	}
}
