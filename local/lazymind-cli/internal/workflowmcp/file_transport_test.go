package workflowmcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestToolFileTransportOnMCPWire(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(root, "arguments.json")
	_ = os.WriteFile(argsPath, []byte(`{"pages":[{"html":"<html>exact worker content</html>","notes":"exact notes"}]}`), 0600)
	data := []byte("actual file bytes")
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	var sent map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		artifact := map[string]any{"artifact_id": "saved", "slot": "preview_html", "revision": 2, "content_type": "text", "value": "saved body must stay out of controller"}
		if strings.Contains(r.URL.Path, "/workflow-artifacts/") {
			_ = json.NewEncoder(w).Encode(artifact)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/artifacts") {
			_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": []any{artifact}})
			return
		}
		if strings.Contains(r.URL.Path, "/workflow-input-resources/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"resource_id": "source", "name": "source.txt", "content_base64": base64.StdEncoding.EncodeToString([]byte("private source body"))})
			return
		}
		if strings.HasSuffix(r.URL.Path, ":begin") {
			_ = json.NewEncoder(w).Encode(map[string]any{"execution_id": "attempt", "execution_handle": "private-grant", "executor_host": "external-agent",
				"step_contract": map[string]any{"prompt": "complete pinned prompt", "execution": map[string]any{"skill_files": []any{map[string]any{"content": "complete pinned Skill"}}}, "required_outputs": []string{"preview_html"}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/projection") {
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "run"})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ready", "files": []any{
			map[string]any{"name": "deck.pptx", "mime_type": "application/octet-stream", "content_base64": base64.StdEncoding.EncodeToString(data), "sha256": hash},
		}}})
	}))
	defer upstream.Close()
	store, _ := credentials.NewStore(t.TempDir(), "")
	if err := store.Save(credentials.Credentials{ServerURL: upstream.URL, AccessToken: "test-access", RefreshToken: "test-refresh"}); err != nil {
		t.Fatal(err)
	}
	api, _ := coreapi.New(store)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Register(server, &Client{api: api})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "workflow.tool.call", Arguments: map[string]any{
		"session_id": "run", "execution_id": "attempt", "execution_handle": "grant", "tool_name": "ppt_export_html",
		"command_id": "same-command", "arguments_file": argsPath, "result_directory": root,
	}})
	if err != nil || result.IsError {
		t.Fatalf("file call failed: %+v %v", result, err)
	}
	if sent["arguments_file"] != nil || sent["result_directory"] != nil || sent["execution_handle"] != "grant" || sent["command_id"] != "same-command" {
		t.Fatalf("transport paths leaked or authority lost: %v", sent)
	}
	args := sent["arguments"].(map[string]any)
	if args["pages"].([]any)[0].(map[string]any)["html"] != "<html>exact worker content</html>" {
		t.Fatal("arguments changed")
	}
	raw, _ := json.Marshal(result.StructuredContent)
	if strings.Contains(string(raw), "content_base64") || strings.Contains(string(raw), "actual file bytes") {
		t.Fatal("full result entered controller payload")
	}
	var summary map[string]any
	_ = json.Unmarshal(raw, &summary)
	full, err := os.ReadFile(summary["result_path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), "content_base64") {
		t.Fatal("full result not persisted")
	}
	file := summary["files"].([]any)[0].(map[string]any)
	decoded, err := os.ReadFile(file["local_path"].(string))
	if err != nil || string(decoded) != string(data) {
		t.Fatal("actual file did not transfer")
	}
	claimed, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "workflow.step.claim", Arguments: map[string]any{
		"session_id": "run", "execution_id": "attempt", "contract_directory": root,
	}})
	if err != nil || claimed.IsError {
		t.Fatalf("claim file mode: %+v %v", claimed, err)
	}
	wire, _ := json.Marshal(claimed.StructuredContent)
	if strings.Contains(string(wire), "complete pinned") || !strings.Contains(string(wire), "private-grant") || !strings.Contains(string(wire), "step_contract_file") {
		t.Fatalf("controller payload invalid: %s", wire)
	}
	for _, content := range claimed.Content {
		if item, ok := content.(*mcp.TextContent); ok && strings.Contains(item.Text, "complete pinned") {
			t.Fatal("MCP text fallback leaked full contract")
		}
	}
	for _, call := range []struct {
		name string
		args map[string]any
		ref  string
	}{
		{"workflow.artifact.get", map[string]any{"artifact_id": "saved", "result_directory": root}, "artifact_file"},
		{"workflow.input.get", map[string]any{"resource_id": "source", "result_directory": root}, "resource_file"},
	} {
		got, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: call.name, Arguments: call.args})
		if err != nil || got.IsError {
			t.Fatalf("%s file transport failed: %v %+v", call.name, err, got)
		}
		raw, _ := json.Marshal(got.StructuredContent)
		if strings.Contains(string(raw), "saved body") || strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte("private source body"))) {
			t.Fatal("resource body leaked")
		}
		var summary map[string]any
		_ = json.Unmarshal(raw, &summary)
		ref := summary[call.ref].(map[string]any)
		if info, err := os.Stat(ref["result_path"].(string)); err != nil || info.Size() == 0 {
			t.Fatal("resource not saved")
		}
	}
	listed, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "workflow.artifact.list", Arguments: map[string]any{"session_id": "run", "metadata_only": true}})
	if err != nil || listed.IsError {
		t.Fatalf("list metadata failed: %v %+v", err, listed)
	}
	listJSON, _ := json.Marshal(listed.StructuredContent)
	if strings.Contains(string(listJSON), "saved body") || !strings.Contains(string(listJSON), `"artifact_id":"saved"`) {
		t.Fatal("metadata inventory lost identity or leaked body")
	}
}

func TestContractFileKeepsGrantOutAndPreservesPinnedBusinessContract(t *testing.T) {
	full := BeginResult{Execution: Execution{ExecutionID: "attempt", ExecutionHandle: "secret-grant", ExecutorHost: "external-agent",
		StepContract: StepContract{StepID: "generate", WorkflowRevision: "pinned", Prompt: "full task", Execution: map[string]any{"skill_files": []any{map[string]any{"content": "full skill"}}}, RequiredOutputs: []string{"preview_html"}, OutputCardinality: map[string]string{"preview_html": "list"}}}}
	result, err := withContractFile(t.TempDir(), func() (BeginResult, error) { return full, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.ExecutionHandle != "secret-grant" || result.Execution.StepContract.Execution != nil || result.Execution.StepContract.Prompt != "" {
		t.Fatal("controller lost grant or received full business context")
	}
	raw, err := os.ReadFile(result.Execution.StepContractFile["result_path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-grant") || !strings.Contains(string(raw), "full skill") {
		t.Fatal("worker contract lost Skill or received grant")
	}
	var restored StepContract
	_ = json.Unmarshal(raw, &restored)
	if restored.WorkflowRevision != "pinned" || restored.Prompt != "full task" || len(restored.RequiredOutputs) != 1 || result.Execution.StepContract.OutputCardinality["preview_html"] != "list" {
		t.Fatal("pinned contract changed")
	}
	called := false
	_, err = withContractFile("relative", func() (BeginResult, error) { called = true; return full, nil })
	if err == nil || called {
		t.Fatal("invalid directory admitted a state mutation")
	}
}

func TestValueFilesPublishExactTextAndJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value")
	_ = os.WriteFile(path, []byte("<html>完整文本\n</html>"), 0600)
	result, err := encodeOutput(Output{Slot: "preview_html", ContentType: "text", ValueFile: path, Seq: 2})
	if err != nil || result["value"] != "<html>完整文本\n</html>" || result["seq"] != 2 {
		t.Fatalf("text %v %v", result, err)
	}
	_ = os.WriteFile(path, []byte(`{"questions":[]}`), 0600)
	result, err = encodeOutput(Output{Slot: "clarification", ContentType: "json", ValueFile: path, Seq: 1})
	if err != nil || len(result["value"].(map[string]any)["questions"].([]any)) != 0 {
		t.Fatalf("json %v %v", result, err)
	}
	for _, output := range []Output{
		{Slot: "x", ContentType: "text", ValueFile: path, Value: "conflict", Seq: 1},
		{Slot: "x", ContentType: "text", ValueFile: path, LocalPath: path, Seq: 1},
		{Slot: "x", ContentType: "file", ValueFile: path, Seq: 1},
	} {
		if _, err := encodeOutput(output); err == nil {
			t.Fatal("invalid publication accepted")
		}
	}
}

func TestToolFilesRejectCorruptionTraversalAndAmbiguity(t *testing.T) {
	for _, file := range []map[string]any{
		{"name": "../escape", "content_base64": "YQ=="},
		{"name": "ok", "content_base64": "YQ==", "sha256": "wrong"},
		{"name": "ok", "content_base64": "invalid!"},
		{"name": "response.json", "content_base64": "YQ=="},
	} {
		root := t.TempDir()
		if _, err := saveExecutionResult(root, map[string]any{"result": map[string]any{"files": []any{file}}}); err == nil {
			t.Fatal("unsafe file accepted")
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 0 {
			t.Fatal("failed result left partial files")
		}
	}
	client := &Client{}
	_, err := client.CallExecutionTool(context.Background(), ExecutionToolCallInput{SessionID: "s", ExecutionID: "e", ExecutionHandle: "grant", ToolName: "tool", CommandID: "id", Arguments: map[string]any{}, ArgumentsFile: "/not/read"})
	if err == nil {
		t.Fatal("ambiguous arguments accepted")
	}
}
