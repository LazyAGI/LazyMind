package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/coreapi"
)

func TestInvocationMiddlewareRecordsRealMCPCall(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	recorder := &recordingInvocationRecorder{}
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind", Version: "test"}, nil)
	handlerSawContext := false
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.begin"},
		func(callCtx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			metadata, ok := coreapi.InvocationFromContext(callCtx)
			handlerSawContext = ok && metadata.ClientName == "codex-cli" && metadata.ID != ""
			return nil, map[string]any{"execution": map[string]any{
				"execution_id": "attempt-1", "step_contract": map[string]any{"session_id": "session-1", "step_id": "draft"},
			}}, nil
		})
	server.AddReceivingMiddleware(invocationMiddleware(recorder, "connector-1", map[string]bool{"workflow.step.begin": false}))
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "codex-cli", Version: "1.2.3"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.step.begin", Arguments: map[string]any{
		"session_id": "session-1", "step_id": "draft", "objective": "do not persist this prompt",
	}, Meta: mcp.Meta{
		"threadId":              "codex-thread-1",
		"callId":                "tool-call-must-not-replace-turn",
		"x-codex-turn-metadata": map[string]any{"turn_id": "codex-turn-1", "thread_source": "user"},
	}})
	if err != nil || result.IsError {
		t.Fatalf("call tool: result=%+v err=%v", result, err)
	}
	if !handlerSawContext {
		t.Fatal("tool handler did not receive invocation context")
	}
	starts, finishes := recorder.values()
	if len(starts) != 1 || len(finishes) != 1 {
		t.Fatalf("records: starts=%d finishes=%d", len(starts), len(finishes))
	}
	if starts[0].ClientName != "codex-cli" || starts[0].ClientVersion != "1.2.3" ||
		starts[0].ToolName != "workflow.step.begin" || starts[0].ReadOnly {
		t.Fatalf("start evidence: %+v", starts[0])
	}
	if starts[0].Source == nil || starts[0].Source.Provider != "codex" ||
		starts[0].Source.ThreadID != "codex-thread-1" || starts[0].Source.TurnID != "codex-turn-1" {
		t.Fatalf("source context: %+v", starts[0].Source)
	}
	if strings.Contains(string(starts[0].RequestSummary), "do not persist") ||
		!strings.Contains(string(starts[0].RequestSummary), `"objective_length":26`) {
		t.Fatalf("unsafe request summary: %s", starts[0].RequestSummary)
	}
	if finishes[0].Status != "succeeded" || finishes[0].SessionID != "session-1" ||
		finishes[0].StepID != "draft" || finishes[0].AttemptID != "attempt-1" {
		t.Fatalf("finish evidence: %+v", finishes[0])
	}
}

func TestInvocationMiddlewareFailsClosedBeforeToolSideEffect(t *testing.T) {
	recorder := &recordingInvocationRecorder{startErr: errors.New("ledger unavailable")}
	called := false
	middleware := invocationMiddleware(recorder, "connector-1", nil)
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "knowledge.list"}}
	_, err := middleware(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		called = true
		return &mcp.CallToolResult{}, nil
	})(context.Background(), "tools/call", request)
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestEvidenceSummaryExcludesContentAndAbsolutePath(t *testing.T) {
	_, summary := summarizeArguments(json.RawMessage(`{
		"query":"private search text",
		"local_path":"/Users/example/secret/image.png",
		"knowledge_ids":["kb-1","kb-2"],
		"outputs":[{"slot":"image","content_base64":"very-secret-bytes"}],
		"arbitrary_private_key":["private-list-value"]
	}`))
	text := string(summary)
	for _, forbidden := range []string{"private search text", "/Users/example", "very-secret-bytes", "private-list-value", "arbitrary_private_key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{`"query_length":19`, `"local_file_name":"image.png"`, `"knowledge_ids":["kb-1","kb-2"]`, `"outputs_count":1`, `"slot":"image"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("summary missing %s: %s", expected, text)
		}
	}
}

func TestInvocationSourceMessageUsesUserFacingToolFields(t *testing.T) {
	if got := invocationSourceMessage("knowledge.search", json.RawMessage(`{"query":"原始问题"}`)); got != "原始问题" {
		t.Fatalf("knowledge source message=%q", got)
	}
	if got := invocationSourceMessage("workflow.start", json.RawMessage(`{"request_context":"生成报告"}`)); got != "生成报告" {
		t.Fatalf("Workflow source message=%q", got)
	}
	if got := invocationSourceMessage("knowledge.list", json.RawMessage(`{"keyword":"private"}`)); got != "" {
		t.Fatalf("list tool leaked non-message field: %q", got)
	}
}

type recordingInvocationRecorder struct {
	mu        sync.Mutex
	startErr  error
	starts    []coreapi.InvocationStart
	finishes  []coreapi.InvocationFinish
	startIDs  []string
	finishIDs []string
}

func (r *recordingInvocationRecorder) StartInvocation(_ context.Context, id string, input coreapi.InvocationStart) (coreapi.InvocationStartResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startIDs = append(r.startIDs, id)
	r.starts = append(r.starts, input)
	if r.startErr != nil {
		return coreapi.InvocationStartResult{}, r.startErr
	}
	return coreapi.InvocationStartResult{Source: &coreapi.InvocationSourceLink{
		ConversationID: "conversation-1", ExternalRef: "run-1",
	}}, nil
}

func (r *recordingInvocationRecorder) FinishInvocation(_ context.Context, id string, input coreapi.InvocationFinish) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishIDs = append(r.finishIDs, id)
	r.finishes = append(r.finishes, input)
	return nil
}

func (r *recordingInvocationRecorder) values() ([]coreapi.InvocationStart, []coreapi.InvocationFinish) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]coreapi.InvocationStart(nil), r.starts...), append([]coreapi.InvocationFinish(nil), r.finishes...)
}
