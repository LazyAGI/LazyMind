package workflowmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestConnectorRenewsClaimedStepBeyondTheMCPCancel(t *testing.T) {
	heartbeat := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/core/workflow-sessions/run/hosted-attempts/step:begin":
			_ = json.NewEncoder(w).Encode(Execution{ExecutionID: "step", ExecutionHandle: "handle", LeaseExpires: time.Now().Add(3 * time.Second).Format(time.RFC3339Nano)})
		case "/api/core/workflow-sessions/run/projection":
			_, _ = w.Write([]byte(`{"session_id":"run","control":{"protocol":"workflow.control.v1"}}`))
		case "/api/core/workflow-sessions/run/hosted-attempts/step:heartbeat":
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]time.Time{"lease_expires_at": time.Now().Add(30 * time.Minute)})
			heartbeat <- input["execution_handle"]
		case "/api/core/workflow-sessions/run/hosted-attempts/step:complete":
			_, _ = w.Write([]byte(`{"execution_id":"step","attempt_status":"succeeded"}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	store, err := credentials.NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: server.URL, AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	api, err := coreapi.New(store)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := api.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{api: api}
	client.EnableStepLeaseRenewal(t.Context(), scope)
	call, cancel := context.WithCancel(t.Context())
	_, err = client.Claim(call, ResumeInput{SessionID: "run", ExecutionID: "step"})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case handle := <-heartbeat:
		if handle != "handle" {
			t.Fatalf("heartbeat had incorrect handle: %q", handle)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the connector did not renew the step lease")
	}
	if _, err := client.Complete(t.Context(), CompleteInput{SessionID: "run", ExecutionID: "step", ExecutionHandle: "handle", Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	client.leaseRenewal.mu.Lock()
	defer client.leaseRenewal.mu.Unlock()
	if len(client.leaseRenewal.steps) != 0 {
		t.Fatal("completed execution still has a lease renewal")
	}
}
