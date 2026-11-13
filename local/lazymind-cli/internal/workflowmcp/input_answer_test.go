package workflowmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestMCPInputAnswerPreservesUserReplyAndQuestionVersion(t *testing.T) {
	var requests []map[string]any
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/core/workflow-sessions/run/projection" {
			_, _ = w.Write([]byte(`{"session_id":"run","state_version":4,"control":{"continuation":"awaiting_user","admission":{"can_begin":false},"input_request":{"artifact_id":"question","revision":2,"questions":[{"id":"date","text":"When?"}]}}}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/core/workflow-sessions/run/control" {
			t.Errorf("unexpected endpoint %s %s", r.Method, r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		requests = append(requests, input)
		_, _ = w.Write([]byte(`{"receipt":{"kind":"answer_input","command_id":"answer-1"},"control":{"continuation":"stopped","admission":{"can_begin":false}}}`))
	}))
	defer apiServer.Close()
	store, err := credentials.NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: apiServer.URL, AccessToken: "test", RefreshToken: "test-refresh"}); err != nil {
		t.Fatal(err)
	}
	api, err := coreapi.New(store)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{api: api}
	state, err := client.State(t.Context(), "run")
	if err != nil || !strings.Contains(state.AgentInstruction, "workflow.input.answer") || !strings.Contains(state.AgentInstruction, "actual reply") {
		t.Fatalf("question guidance=%+v err=%v", state, err)
	}
	ct, st := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Register(server, client)
	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	args := map[string]any{"session_id": "run", "input_artifact_id": "question", "input_revision": 2, "expected_state_version": 4, "answer_text": "用户说：日期未知，保留待确认。", "command_id": "answer-1"}
	for range 2 {
		result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "workflow.input.answer", Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("answer=%+v err=%v", result, err)
		}
	}
	want := map[string]any{"kind": "answer_input", "input_artifact_id": "question", "input_revision": float64(2), "expected_state_version": float64(4), "answer_text": args["answer_text"], "command_id": "answer-1"}
	if len(requests) != 2 || !reflect.DeepEqual(requests[0], want) || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("answer identity changed: %+v", requests)
	}
	_, err = (&Client{}).AnswerInput(context.Background(), InputAnswer{SessionID: "run", ArtifactID: "q", Revision: 1, CommandID: "answer"})
	if err == nil {
		t.Fatal("missing actual user reply admitted")
	}
}

func TestSharedExecutionContractSurvivesMCP(t *testing.T) {
	var contract StepContract
	if err := json.Unmarshal([]byte(`{"prompt":"one task","execution":{"version":"1","skill_files":[{"path":"scenario/execution.md","content":"shared"}],"constraints":["exact count"]}}`), &contract); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(contract)
	if err != nil || !strings.Contains(string(encoded), `"execution":`) || !strings.Contains(string(encoded), `"content":"shared"`) {
		t.Fatalf("shared contract lost: %s %v", encoded, err)
	}
}
