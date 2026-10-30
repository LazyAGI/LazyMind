package workflowmcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func readinessClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
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
	return &Client{api: api}
}

func TestExecutionToolsRejectDisconnectedHostWithoutCoreRequests(t *testing.T) {
	ctx := context.Background()
	requests, checks := 0, 0
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) { requests++; t.Error("disconnected tool contacted Core") })
	client.HostProvider = "codex"
	client.EnsureHostReady = func(context.Context) error {
		checks++
		return errors.New("CODEX_CONNECTION_REQUIRED: Open LazyMind > Codex > Installation guide")
	}
	a, b := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Register(server, client)
	ss, err := server.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"workflow.start", map[string]any{"workflow_id": "wf", "idempotency_key": "retry-start", "driver_session_id": threadUUID}},
		{"workflow.step.begin", map[string]any{"session_id": "old-run", "step_id": "step", "command_id": "stable-command"}},
		{"workflow.step.claim", map[string]any{"session_id": "old-run", "execution_id": "old-execution"}},
		{"workflow.step.resume", map[string]any{"session_id": "old-run", "execution_id": "old-execution"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := checks
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
			if err != nil || !result.IsError || checks != previous+1 {
				t.Fatalf("result=%+v err=%v checks=%d", result, err, checks)
			}
			if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "Installation guide") {
				t.Fatal("connection guidance lost")
			}
		})
	}
	if requests != 0 {
		t.Fatal("disconnected execution changed workflow state")
	}
}

func TestBeginChecksOnceAndRecoveryRechecksOriginalExecution(t *testing.T) {
	checks, grants := 0, 0
	var paths []string
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if checks == 0 {
			t.Error("Core called before readiness")
		}
		switch r.URL.Path {
		case "/api/core/workflow-sessions/old-run/projection":
			w.Write([]byte(`{"session_id":"old-run","state_version":7,"control":{"protocol":"workflow.control.v1"}}`))
		case "/api/core/workflow-sessions/old-run/executions:begin":
			grants++
			w.Write([]byte(`{"receipt":{"execution_id":"old-execution"}}`))
		case "/api/core/workflow-sessions/old-run/hosted-attempts/old-execution:begin", "/api/core/workflow-sessions/old-run/hosted-attempts/old-execution:resume":
			w.Write([]byte(`{"execution_id":"old-execution"}`))
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	disconnected := false
	client.EnsureHostReady = func(context.Context) error {
		checks++
		if disconnected {
			return errors.New("disconnected")
		}
		return nil
	}
	ctx := context.Background()
	if _, err := client.Begin(ctx, BeginInput{SessionID: "old-run", StepID: "step", CommandID: "stable-command"}); err != nil {
		t.Fatal(err)
	}
	if checks != 1 || grants != 1 {
		t.Fatalf("checks=%d grants=%d", checks, grants)
	}
	before := append([]string(nil), paths...)
	disconnected = true
	if _, err := client.Resume(ctx, ResumeInput{SessionID: "old-run", ExecutionID: "old-execution"}); err == nil {
		t.Fatal("recovery trusted previous check")
	}
	if checks != 2 || !reflect.DeepEqual(before, paths) {
		t.Fatal("disconnected recovery contacted Core")
	}
	disconnected = false
	if _, err := client.Resume(ctx, ResumeInput{SessionID: "old-run", ExecutionID: "old-execution"}); err != nil {
		t.Fatal(err)
	}
	if checks != 3 || grants != 1 {
		t.Fatal("recovery recreated a grant")
	}
	if _, err := client.Claim(ctx, ResumeInput{SessionID: "old-run", ExecutionID: "old-execution"}); err != nil {
		t.Fatal(err)
	}
	if checks != 4 || grants != 1 {
		t.Fatal("claim reused old readiness or advanced workflow")
	}
}

func TestReadCompletionAndStopRemainAvailableWithoutControlConnection(t *testing.T) {
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/executions:stop") {
			w.Write([]byte(`{"control":{"session_id":"old-run","continuation":"stopped"}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/projection") {
			w.Write([]byte(`{"session_id":"old-run","control":{"protocol":"workflow.control.v1"}}`))
			return
		}
		w.Write([]byte(`{}`))
	})
	client.EnsureHostReady = func(context.Context) error {
		t.Error("non-execution operation gated")
		return errors.New("disconnected")
	}
	ctx := context.Background()
	if _, err := client.State(ctx, "old-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListArtifacts(ctx, "old-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetArtifact(ctx, "artifact"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(ctx, CompleteInput{SessionID: "old-run", ExecutionID: "old-execution", Outcome: "completed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StopSession(ctx, "old-run", "stop-command"); err != nil {
		t.Fatal(err)
	}
}
