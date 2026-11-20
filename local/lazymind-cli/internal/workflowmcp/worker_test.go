package workflowmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestWorkerMCPExposesOnlyBoundToolOperations(t *testing.T) {
	calls := 0
	toolCalls := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["execution_handle"] != "restricted-worker" {
			t.Error("worker lost its restricted identity")
		}
		if r.URL.Path == "/api/core/workflow-sessions/run/hosted-attempts/attempt/tools:call" {
			toolCalls++
			if body["tool_name"] == "invalid_candidate" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"INVALID_ARGUMENT","message":"candidate needs repair","retryable":false}}`))
				return
			}
			if body["tool_name"] != "paper_read_material" || body["command_id"] == "" {
				t.Error("lost worker business call")
			}
		} else if r.URL.Path != "/api/core/workflow-sessions/run/hosted-attempts/attempt/tools:list" {
			t.Errorf("worker escaped bound endpoint: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"tools":[]}`))
	}))
	defer apiServer.Close()
	store, err := credentials.NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: apiServer.URL, AccessToken: "test", RefreshToken: "test"}); err != nil {
		t.Fatal(err)
	}
	api, err := coreapi.New(store)
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "worker-test", Version: "1"}, nil)
	RegisterToolWorker(server, &Client{api: api}, workerConnection{"run", "attempt", "restricted-worker"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 2 {
		t.Fatalf("unexpected worker surface: %+v %v", listed, err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "workflow.tool.list" && tool.Name != "workflow.tool.call" {
			t.Fatalf("control tool leaked: %s", tool.Name)
		}
	}
	for range 2 {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.tool.list", Arguments: map[string]any{}})
		if err != nil || result.IsError {
			t.Fatalf("worker direct discovery failed: %+v %v", result, err)
		}
	}
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.step.complete", Arguments: map[string]any{}}); err == nil {
		t.Fatal("worker server accepted a control operation")
	}
	for _, command := range []string{"page-1", "page-2"} {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.tool.call", Arguments: map[string]any{
			"tool_name": "paper_read_material", "arguments": map[string]any{"material_id": "p1"}, "command_id": command}})
		if err != nil || result.IsError {
			t.Fatalf("direct business call: %v %v", result, err)
		}
	}
	failure, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.tool.call", Arguments: map[string]any{
		"tool_name": "invalid_candidate", "arguments": map[string]any{}, "command_id": "invalid-candidate"}})
	if err != nil || !failure.IsError {
		t.Fatalf("structured failure missing: %v %v", failure, err)
	}
	raw, _ := json.Marshal(failure.StructuredContent)
	if !strings.Contains(string(raw), "INVALID_ARGUMENT") {
		t.Fatal("worker lost actionable error code")
	}
	if toolCalls != 3 {
		t.Fatal("worker did not call consecutive business tools")
	}
	if calls != 5 {
		t.Fatalf("unexpected upstream calls: %d", calls)
	}
}
