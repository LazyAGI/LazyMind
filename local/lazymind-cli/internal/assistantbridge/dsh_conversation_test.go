package assistantbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/chatagent"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestDSHConversationRelaysOnlyWithCurrentAccountAndPairedProfile(t *testing.T) {
	var received struct {
		HostID   string `json:"host_id"`
		Sessions []chatagent.NativeSession
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/core/external-chat/providers/deepseek-harness/sessions:sync" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"updated":1,"rejected":0}`))
	}))
	defer upstream.Close()
	home := t.TempDir()
	s := newTestServer(t, home)
	if err := s.store.Save(credentials.Credentials{ServerURL: upstream.URL, AccessToken: "test", RefreshToken: "refresh", SavedAt: float64(time.Now().Unix()), ExpiresIn: 3600}); err != nil {
		t.Fatal(err)
	}
	scope, err := s.store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "desktop")
	pair, err := workflowhost.Ensure(home, profile, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connectorconfig.Save(connectorconfig.Config{Version: 1, Home: home, Provider: pair.Provider, HostID: "real-host", Profile: profile, Controlled: true, PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")}); err != nil {
		t.Fatal(err)
	}
	submit := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/workflow-host/conversation", strings.NewReader(`{"thread_id":"root","host_id":"spoof","cwd":"/testing/project","display_name":"topic","turns":[{"turn_id":"m1","user":"hello","assistant":"answer"}]}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
		resp := httptest.NewRecorder()
		s.routes().ServeHTTP(resp, req)
		return resp.Code
	}
	if status := submit("wrong"); status != 403 || calls != 0 {
		t.Fatalf("invalid credential: status=%d upstream=%d", status, calls)
	}
	if status := submit(pair.Token); status != 200 || calls != 1 || received.HostID != "real-host" || len(received.Sessions) != 1 {
		t.Fatalf("valid credential: status=%d upstream=%d", status, calls)
	}
	record := received.Sessions[0]
	if record.ThreadID != "root" || record.ProjectName != "project" || record.TurnCount != 1 || record.Turns[0].Assistant != "answer" {
		t.Fatalf("bad transcript projection %+v", record)
	}
}
