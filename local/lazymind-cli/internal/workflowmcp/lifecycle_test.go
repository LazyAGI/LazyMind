package workflowmcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestResumeSessionReturnsScheduledExecution(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/core/workflow-sessions/run:resume" || r.Header.Get("Origin") != "" {
			t.Errorf("unexpected resume request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input SessionLifecycleInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.CommandID != "resume" {
			t.Errorf("lost retry identity: %+v", input)
		}
		calls++
		_, _ = w.Write([]byte(`{"session_id":"run","status":"active","state_version":9,"command_id":"resume","execution_id":"attempt-2","action_id":"wake-controller"}`))
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
	client := &Client{api: api}
	first, err := client.ResumeSession(t.Context(), "run", "resume")
	if err != nil || first.Status != "active" || first.ExecutionID != "attempt-2" || first.ActionID != "wake-controller" {
		t.Fatalf("lost recovery receipt: %+v %v", first, err)
	}
	replay, err := client.ResumeSession(t.Context(), "run", "resume")
	if err != nil || replay != first || calls != 2 {
		t.Fatalf("resume retry changed: %+v calls=%d err=%v", replay, calls, err)
	}
}
