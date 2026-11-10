package workflowmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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
			if input["step_id"] != "step" || input["expected_state_version"] != float64(7) || input["retry_origin"] != "user" {
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
	_, err := client.Begin(context.Background(), BeginInput{SessionID: "run", StepID: "step", CommandID: "begin", RetryOrigin: "user"})
	var rejected *coreapi.Error
	if !submitted || !errors.As(err, &rejected) || rejected.StatusCode != http.StatusConflict || rejected.Code != "WORKFLOW_ADMISSION_DENIED" || rejected.Message != "Core rejected step" {
		t.Fatalf("MCP must forward to Core and preserve rejection: submitted=%v err=%v", submitted, err)
	}
}

func TestBeginRetryUsesTheSameClaimAfterALostResponse(t *testing.T) {
	var claims atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/core/workflow-sessions/run/projection":
			fmt.Fprintf(w, `{"session_id":"run","state_version":%d,"control":{"protocol":"workflow.control.v1"}}`, 4+claims.Load())
		case "/api/core/workflow-sessions/run/executions:begin":
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["command_id"] != "begin-1" || input["expected_state_version"] != float64(4+claims.Load()) {
				t.Errorf("begin request lost its identity or precondition: %v", input)
			}
			_, _ = w.Write([]byte(`{"receipt":{"execution_id":"attempt"}}`))
		case "/api/core/workflow-sessions/run/hosted-attempts/attempt:begin":
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["claim_id"] != "begin-1" {
				t.Errorf("claim lost its replay key: %v %v", input, err)
			}
			if claims.Add(1) == 1 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			_, _ = w.Write([]byte(`{"execution_id":"attempt","execution_handle":"original-handle"}`))
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
	client := &Client{api: api}
	input := BeginInput{SessionID: "run", StepID: "draft", CommandID: "begin-1"}
	if _, err := client.Begin(t.Context(), input); err == nil {
		t.Fatal("first claim response was supposed to be lost")
	}
	result, err := client.Begin(t.Context(), input)
	if err != nil || result.Execution.ExecutionID != "attempt" || result.Execution.ExecutionHandle != "original-handle" || claims.Load() != 2 {
		t.Fatalf("begin retry did not recover the execution: %+v %v", result, err)
	}
}
