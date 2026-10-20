package workflowmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestBeginLeavesStepAdmissionToCore(t *testing.T) {
	submitted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/core/workflow-sessions/run/projection":
			_, _ = w.Write([]byte(`{"session_id":"run","state_version":7,"projection":{"ready":[]},"control":{"protocol":"workflow.control.v1"}}`))
		case "/api/core/workflow-sessions/run/executions:begin":
			submitted = true
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["step_id"] != "step" || input["expected_state_version"] != float64(7) {
				t.Errorf("wrong request: %v", input)
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"WORKFLOW_ADMISSION_DENIED","message":"Core rejected step"}}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	store, _ := credentials.NewStore(t.TempDir(), "")
	if err := store.Save(credentials.Credentials{ServerURL: server.URL, AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	api, _ := coreapi.New(store)
	client := &Client{api: api}
	_, err := client.Begin(context.Background(), BeginInput{SessionID: "run", StepID: "step", CommandID: "begin"})
	var rejected *coreapi.Error
	if !submitted || !errors.As(err, &rejected) || rejected.StatusCode != http.StatusConflict || rejected.Code != "WORKFLOW_ADMISSION_DENIED" || rejected.Message != "Core rejected step" {
		t.Fatalf("MCP must forward to Core and preserve rejection: submitted=%v err=%v", submitted, err)
	}
}
