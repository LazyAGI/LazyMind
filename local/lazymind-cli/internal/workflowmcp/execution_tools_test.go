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

func TestExecutionToolsMCPPreservesAuthorizationAndIdempotency(t *testing.T) {
	var requests []map[string]any
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/core/workflow-sessions/run-1/hosted-attempts/attempt-1/tools:") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		requests = append(requests, input)
		if strings.HasSuffix(r.URL.Path, ":list") {
			_, _ = w.Write([]byte(`{"tools":[{"name":"render","source":"package","input_schema":{"type":"object"}}]}`))
			return
		}
		if input["command_id"] == "invalid-input" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"INVALID_ARGUMENT","message":"start_page must be between 1 and 5."}}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":{"resource_id":"output-1"},"receipt":{"command_id":"command-1"}}`))
	}))
	defer apiServer.Close()
	store, err := credentials.NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: apiServer.URL, AccessToken: "test-access", RefreshToken: "test-refresh"}); err != nil {
		t.Fatal(err)
	}
	api, err := coreapi.New(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Register(server, &Client{api: api})
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	base := map[string]any{"session_id": "run-1", "execution_id": "attempt-1", "execution_handle": "opaque-grant"}
	listed, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.tool.list", Arguments: base})
	if err != nil || listed.IsError {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	base["tool_name"], base["source"], base["command_id"] = "render", "package", "command-1"
	base["arguments"] = map[string]any{"input": "resource:input-1"}
	for range 2 {
		called, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.tool.call", Arguments: base})
		if err != nil || called.IsError {
			t.Fatalf("call=%+v err=%v", called, err)
		}
		body, _ := json.Marshal(called.StructuredContent)
		if !strings.Contains(string(body), `"resource_id":"output-1"`) || !strings.Contains(string(body), `"command_id":"command-1"`) {
			t.Fatalf("lost result fields: %s", body)
		}
	}
	if len(requests) != 3 || !reflect.DeepEqual(requests[1], requests[2]) || requests[0]["execution_handle"] != "opaque-grant" {
		t.Fatalf("incorrect dispatch/retry: %#v", requests)
	}
	if requests[1]["command_id"] != "command-1" || requests[1]["source"] != "package" || requests[1]["tool_name"] != "render" {
		t.Fatalf("lost call scope: %#v", requests[1])
	}
	base["command_id"] = "invalid-input"
	rejected, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow.tool.call", Arguments: base})
	if err != nil || !rejected.IsError {
		t.Fatalf("expected MCP tool error: %+v %v", rejected, err)
	}
	body, _ := json.Marshal(rejected.Content)
	if !strings.Contains(string(body), "INVALID_ARGUMENT: start_page must be between 1 and 5.") {
		t.Fatalf("lost remediation in MCP response: %s", body)
	}
}

func TestExecutionToolRejectsIncompleteAuthorityBeforeHTTP(t *testing.T) {
	client := &Client{}
	valid := ExecutionToolCallInput{SessionID: "run", ExecutionID: "attempt", ExecutionHandle: "grant", ToolName: "render", CommandID: "command", Arguments: map[string]any{}}
	for _, mutate := range []func(*ExecutionToolCallInput){
		func(in *ExecutionToolCallInput) { in.ExecutionHandle = "" },
		func(in *ExecutionToolCallInput) { in.CommandID = "" },
		func(in *ExecutionToolCallInput) { in.Arguments = nil },
		func(in *ExecutionToolCallInput) { in.Source = "arbitrary" },
	} {
		input := valid
		mutate(&input)
		if _, err := client.CallExecutionTool(context.Background(), input); err == nil {
			t.Fatal("invalid call admitted")
		}
	}
}

func TestExternalExecutionContractRoundTripPreservesPinnedSkillsAndExtensions(t *testing.T) {
	raw := `{"external_execution":{"version":"1","validation_owner":"external-agent","skill_files":[{"path":"skills/design.md","content":"Pinned skill","sha256":"abc"}],"tools":[{"name":"builtin:image_generator","source":"global"}],"future_policy":{"required":false}},"declared_input_transports":{"source":"path"},"declared_output_types":{"deck":"file"}}`
	var contract StepContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_ = json.Unmarshal([]byte(raw), &before)
	_ = json.Unmarshal(encoded, &after)
	for _, key := range []string{"external_execution", "declared_input_transports", "declared_output_types"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatalf("contract field %s changed: %s", key, encoded)
		}
	}
}

// The MCP SDK validates typed outputs against its generated schema after the
// Core mutation succeeds. RawMessage is modeled as a byte array by that schema,
// so a plain JSON roundtrip test cannot detect a lost handle on the MCP wire.
func TestExternalExecutionContractPassesMCPOutputValidation(t *testing.T) {
	skillFiles := []any{map[string]any{"path": "skills/design.md", "content": "Pinned skill", "sha256": "abc"}}
	for name, contract := range map[string]map[string]any{
		"legacy": {"external_execution": map[string]any{"version": "1", "validation_owner": "external-agent",
			"skill_files": skillFiles, "future_policy": map[string]any{"required": false}}},
		"shared": {"prompt": "Pinned task", "objective_ref": "#/prompt", "objective": "Keep all 4 pages",
			"execution":          map[string]any{"version": "1", "skill_files": skillFiles, "constraints": []any{"Exact page count"}},
			"external_execution": map[string]any{"version": "1", "validation_owner": "external-agent", "execution_ref": "#/execution", "adapter_instructions": "Use host tools"}},
	} {
		t.Run(name, func(t *testing.T) { checkExecutionContractMCPOutput(t, contract) })
	}
}

func checkExecutionContractMCPOutput(t *testing.T, expectedContract map[string]any) {
	t.Helper()
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var response any
		switch {
		case strings.HasSuffix(r.URL.Path, "/executions:begin"):
			response = map[string]any{"receipt": map[string]any{"execution_id": "attempt-1"}}
		case strings.HasSuffix(r.URL.Path, "/projection"):
			response = map[string]any{"session_id": "run-1", "control": map[string]any{"protocol": ControlProtocol, "continuation": "awaiting_executor"}}
		case strings.HasSuffix(r.URL.Path, "/hosted-attempts/attempt-1:begin"), strings.HasSuffix(r.URL.Path, "/hosted-attempts/attempt-1:resume"):
			response = map[string]any{"execution_id": "attempt-1", "execution_handle": "unchanged-grant", "executor_host": "external-agent",
				"step_contract": expectedContract}
		case strings.HasSuffix(r.URL.Path, "/hosted-attempts/attempt-1:complete"):
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input["executor_ref"] != "" || input["execution_handle"] != "unchanged-grant" || input["summary"] != "Generated and verified in a separate same-agent pass" {
				t.Errorf("single-agent completion changed: %v", input)
			}
			response = map[string]any{"execution_id": "attempt-1", "attempt_status": "succeeded"}
		default:
			t.Errorf("unexpected Core request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer apiServer.Close()
	store, _ := credentials.NewStore(t.TempDir(), "")
	if err := store.Save(credentials.Credentials{ServerURL: apiServer.URL, AccessToken: "test-access", RefreshToken: "test-refresh"}); err != nil {
		t.Fatal(err)
	}
	api, _ := coreapi.New(store)
	server := mcp.NewServer(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil)
	Register(server, &Client{api: api})
	ct, st := mcp.NewInMemoryTransports()
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
	for _, name := range []string{"workflow.step.begin", "workflow.step.claim", "workflow.step.resume"} {
		arguments := map[string]any{"session_id": "run-1", "execution_id": "attempt-1"}
		if name == "workflow.step.begin" {
			arguments = map[string]any{"session_id": "run-1", "step_id": "design", "command_id": "begin-1"}
		}
		result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
		if err != nil || result.IsError {
			t.Fatalf("%s output validation lost the execution: %+v %v", name, result, err)
		}
		body, _ := json.Marshal(result.StructuredContent)
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		execution := decoded["execution"].(map[string]any)
		contract := execution["step_contract"].(map[string]any)
		if execution["execution_handle"] != "unchanged-grant" {
			t.Fatalf("%s dropped the execution handle", name)
		}
		for key, expected := range expectedContract {
			if !reflect.DeepEqual(contract[key], expected) {
				t.Fatalf("%s dropped or expanded contract field %s", name, key)
			}
		}
		// Keep the SDK's JSON text fallback for hosts without structured-content
		// support. Each representation carries full pinned content exactly once.
		for _, representation := range []any{result.Content, result.StructuredContent} {
			encoded, err := json.Marshal(representation)
			if err != nil || strings.Count(string(encoded), "Pinned skill") != 1 {
				t.Fatalf("%s lost or duplicated pinned Skills in one representation: %v", name, err)
			}
		}
	}
	completed, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "workflow.step.complete", Arguments: map[string]any{
		"session_id": "run-1", "execution_id": "attempt-1", "execution_handle": "unchanged-grant", "outcome": "succeeded",
		"summary": "Generated and verified in a separate same-agent pass",
	}})
	if err != nil || completed.IsError {
		t.Fatalf("completion without a worker reference failed: %+v %v", completed, err)
	}
}
