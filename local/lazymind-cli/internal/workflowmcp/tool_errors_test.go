package workflowmcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkflowMCPPreservesProviderFailureAndRequestID(t *testing.T) {
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/core/workflow-sessions/run/hosted-attempts/attempt/tools:call" {
			t.Fatalf("unexpected endpoint: %s", r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input["execution_handle"] != "unchanged-handle" || input["command_id"] != "same-operation" {
			t.Fatal("connector changed authorization or operation identity")
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"PROVIDER_REJECTED","message":"image_generator failed (HTTP 451): provider rejected this request","retryable":false,"upstream":{"http_status":451,"code":"provider_policy","request_id":"request-123"}}}`))
	})
	server := mcp.NewServer(&mcp.Implementation{Name: "error-test", Version: "1"}, nil)
	registerExecutionTools(server, client)
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "workflow.tool.call", Arguments: map[string]any{
		"session_id": "run", "execution_id": "attempt", "execution_handle": "unchanged-handle",
		"tool_name": "image_generator", "command_id": "same-operation", "arguments": map[string]any{"prompt": "background"},
	}})
	if err != nil || !result.IsError {
		t.Fatalf("provider failure became success or protocol error: %+v %v", result, err)
	}
	for _, representation := range []any{result.StructuredContent, result.Content} {
		body, _ := json.Marshal(representation)
		for _, expected := range []string{"PROVIDER_REJECTED", "provider rejected this request", "451", "request-123", "provider_policy", "retryable"} {
			if !strings.Contains(string(body), expected) {
				t.Fatalf("connector lost %s: %s", expected, body)
			}
		}
	}
}
