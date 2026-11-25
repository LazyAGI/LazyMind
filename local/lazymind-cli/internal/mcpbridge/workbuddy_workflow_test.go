package mcpbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestWorkBuddyIdentityUsesNativeControlDuringCoreAuthorizationFailure(t *testing.T) {
	home := t.TempDir()
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:1", AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	account, err := store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", t.TempDir(), account)
	if err != nil {
		t.Fatal(err)
	}

	const session = "92793298-a697-4245-9185-f866ea19ba38"
	args := json.RawMessage(`{"workflow_id":"test","idempotency_key":"test-workbuddy-call"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workbuddy/resolve" ||
			r.Header.Get("Authorization") != "Bearer "+pair.Token || r.Header.Get("X-LazyMind-Connector-Id") != pair.ConnectorID ||
			json.NewDecoder(r.Body).Decode(&request) != nil || string(request.Arguments) != string(args) {
			t.Error("identity resolution lost the request or pairing")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(workbuddyhost.Identity{SessionID: session, RequestID: "native-request", ToolCallID: "tool-call"})
	}))
	defer server.Close()
	config := workbuddyhost.Config{PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Node: filepath.Join(home, "node"), CDPPort: 9333}
	body, _ := json.Marshal(config)
	if err := localfile.Write(workbuddyhost.ConfigPath(home), body, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	status := workbuddyhost.Status{ConnectorID: pair.ConnectorID, CheckedAt: now, Ready: true, Version: "5.9.3", Cancellation: "session",
		BridgeURL: server.URL, ConnectionEvidence: workflowconnector.ConnectionEvidence{PairingRevision: pair.RuntimeRevision,
			Connection: &workflowconnector.ConnectionObservation{CheckedAt: now, Issue: "lazymind_auth_required"}}}
	b := &Bridge{home: home}
	for _, legacy := range []bool{false, true} {
		if legacy {
			// The old worker folded unrelated Bridge failures into its native
			// Ready bit after the renderer probe succeeded.
			status.ConnectionEvidence = workflowconnector.ConnectionEvidence{}
			status.Ready, status.Issue = false, "lazymind_unavailable"
		}
		body, _ = json.Marshal(status)
		if err := localfile.Write(config.PairingFile+".workbuddy-status.json", body, 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := b.resolveWorkBuddyDriver(context.Background(), args); err != nil || got != session {
			t.Fatalf("legacy=%t: local identity was unavailable during a Core connection failure: %q, %v", legacy, got, err)
		}
	}
}
