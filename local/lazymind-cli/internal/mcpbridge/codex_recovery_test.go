package mcpbridge

import (
	"context"
	"encoding/json"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBlockedCodexOperationKeepsArgumentsAndStableRetryIdentity(t *testing.T) {
	thread := "11111111-2222-3333-4444-555555555555"
	for _, tool := range []string{"workflow.start", "workflow.step.begin", "workflow.step.claim", "workflow.step.resume"} {
		t.Run(tool, func(t *testing.T) {
			call := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool, Meta: mcp.Meta{"threadId": thread}}}
			input := map[string]any{"workflow_id": "ppt", "session_id": "existing-run", "step_id": "outline", "execution_id": "existing-execution", "request_context": "original task", "input_bindings": map[string]any{"source": "resource-1"}}
			intent, err := newCodexRecoveryIntent(call, thread, input)
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]any
			if json.Unmarshal(intent.Arguments, &saved) != nil {
				t.Fatal("arguments not JSON")
			}
			if saved["request_context"] != "original task" || saved["execution_id"] != "existing-execution" || intent.ThreadID != thread {
				t.Fatal("lost original request")
			}
			key := "command_id"
			if tool == "workflow.start" {
				key = "idempotency_key"
			}
			if tool == "workflow.start" || tool == "workflow.step.begin" {
				if value, ok := saved[key].(string); !ok || len(value) < 16 {
					t.Fatal("missing stable retry identity")
				}
				second, err := newCodexRecoveryIntent(call, thread, saved)
				if err != nil {
					t.Fatal(err)
				}
				var retry map[string]any
				json.Unmarshal(second.Arguments, &retry)
				if retry[key] != saved[key] {
					t.Fatal("changed retry identity")
				}
			}
		})
	}
}

func TestRecoveryResolvesMissingMetadataFromWrappedControlBinding(t *testing.T) {
	thread := "11111111-2222-3333-4444-555555555555"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/core/workflow-sessions/completed-run/control" {
			t.Error("unexpected recovery read")
		}
		w.Write([]byte(`{"control":{"binding":{"bound":true,"provider":"codex","driver_session_id":"` + thread + `"}}}`))
	}))
	defer server.Close()
	store, _ := credentials.NewStore(t.TempDir(), "")
	store.Save(credentials.Credentials{ServerURL: server.URL, AccessToken: "access", RefreshToken: "refresh"})
	api, _ := coreapi.New(store)
	b := &Bridge{api: api}
	call := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "workflow.state"}}
	got, err := b.codexRecoveryThread(context.Background(), call, map[string]any{"session_id": "completed-run"})
	if err != nil || got != thread {
		t.Fatalf("wrapped binding lost: %q %v", got, err)
	}
}

func TestCompletedResultReadRestoresPresentationWithoutReplacingItsResult(t *testing.T) {
	call := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "workflow.state"}}
	original := &mcp.CallToolResult{StructuredContent: map[string]any{"session_id": "completed-run", "status": "completed"}}
	reads, restores := 0, 0
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) { reads++; return original, nil }
	result, err := codexReadPresentation(context.Background(), "tools/call", call, next, func(context.Context) (bool, error) { restores++; return true, nil })
	if err != nil || reads != 1 || restores != 1 {
		t.Fatal("completed state was gated")
	}
	value := result.(*mcp.CallToolResult)
	if value.IsError || len(value.Content) != 1 || len(original.Content) != 0 {
		t.Fatal("read failed or original result mutated")
	}
	encoded, _ := json.Marshal(value.StructuredContent)
	if string(encoded) != `{"session_id":"completed-run","status":"completed"}` {
		t.Fatal("lost existing results")
	}
	original.IsError = true
	_, _ = codexReadPresentation(context.Background(), "tools/call", call, next, func(context.Context) (bool, error) { t.Fatal("denied read requested presentation"); return false, nil })
	original.IsError = false
	original.StructuredContent = map[string]any{"control": map[string]any{"continuation": "continue", "admission": map[string]any{"can_begin": true}}}
	_, _ = codexReadPresentation(context.Background(), "tools/call", call, next, func(context.Context) (bool, error) {
		t.Fatal("view recovery preceded execution intent")
		return false, nil
	})
}

func TestRepeatedBlockedCallsDeduplicateWithoutSharingOtherThreads(t *testing.T) {
	call := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "workflow.start", Meta: mcp.Meta{"progressToken": "first"}}}
	first, _ := newCodexRecoveryIntent(call, "11111111-2222-3333-4444-555555555555", map[string]any{"workflow_id": "ppt"})
	call.Params.Meta["progressToken"] = "second"
	second, _ := newCodexRecoveryIntent(call, first.ThreadID, map[string]any{"workflow_id": "ppt"})
	other, _ := newCodexRecoveryIntent(call, "22222222-2222-3333-4444-555555555555", map[string]any{"workflow_id": "ppt"})
	if first.Fingerprint != second.Fingerprint || first.Fingerprint == other.Fingerprint {
		t.Fatal("bad request deduplication scope")
	}
}
