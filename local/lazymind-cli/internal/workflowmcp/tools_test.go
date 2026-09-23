package workflowmcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkflowListPublishesObjectInputSchema(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "schema-test", Version: "1"}, nil)
	Register(server, &Client{})
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test-client", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "workflow.list" {
			continue
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("workflow.list input schema=%#v", tool.InputSchema)
		}
		properties, ok := schema["properties"].(map[string]any)
		if schema["type"] != "object" || !ok || len(properties) != 0 {
			t.Fatalf("workflow.list input schema=%#v", schema)
		}
		return
	}
	t.Fatal("workflow.list tool is missing")
}

func TestWorkflowToolDescriptionsMatchExecutionFlow(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "schema-test", Version: "1"}, nil)
	Register(server, &Client{})
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test-client", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tool := range listed.Tools {
		got[tool.Name] = tool.Description
	}
	if !containsAll(got["workflow.start"], "Next call workflow.step.begin", "workflow_id", "revision_id") {
		t.Fatalf("workflow.start description=%q", got["workflow.start"])
	}
	if !containsAll(got["workflow.get"], "inspecting package scripts", "tool_scripts", "compiled_graph") {
		t.Fatalf("workflow.get description=%q", got["workflow.get"])
	}
	if !containsAll(got["workflow.step.begin"], "execution_handle", "executor_host is lazymind", "no handle is issued") {
		t.Fatalf("workflow.step.begin description=%q", got["workflow.step.begin"])
	}
	for _, name := range []string{"workflow.start", "workflow.step.begin"} {
		if containsAny(got[name], "workflow.get", "legacy_tools") {
			t.Errorf("%s retains obsolete script guidance: %q", name, got[name])
		}
	}
	if !containsAll(got["workflow.artifact.get"], "artifact_id", "slot key", "get_artifact/read_artifact") {
		t.Fatalf("workflow.artifact.get description=%q", got["workflow.artifact.get"])
	}
	if !containsAll(got["workflow.artifact.publish"], "text, json, image, file, or file_list", "local_path", "value", "save_artifact/save_artifacts", "key becomes slot", "immediately") {
		t.Fatalf("workflow.artifact.publish description=%q", got["workflow.artifact.publish"])
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func containsAny(value string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}

func TestReadOnlyClassificationCoversEveryWorkflowTool(t *testing.T) {
	readOnly := map[string]bool{
		"workflow.list": true, "workflow.get": true, "workflow.input.get": true,
		"workflow.state": true, "workflow.session.list": true,
		"workflow.artifact.list": true, "workflow.artifact.get": true,
	}
	if len(ToolNames) != 16 {
		t.Fatalf("tool count=%d, want 16", len(ToolNames))
	}
	for _, name := range ToolNames {
		if IsReadOnlyTool(name) != readOnly[name] {
			t.Fatalf("read-only classification for %s is %v", name, IsReadOnlyTool(name))
		}
	}
}

func TestStartResultJSONIncludesPinnedRevision(t *testing.T) {
	body, err := json.Marshal(StartResult{SessionID: "mcp-1", WorkflowID: "test-workflow", RevisionID: "rev-3"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"workflow_id":"test-workflow"`) || !strings.Contains(string(body), `"revision_id":"rev-3"`) {
		t.Fatalf("start result=%s", body)
	}
}

func TestGeneratedIDsFitWorkflowPersistence(t *testing.T) {
	for _, prefix := range []string{"mcp-start-", "mcp-step-", "mcp-session-"} {
		id, err := newID(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if len(id) > 36 {
			t.Fatalf("generated ID %q has %d characters", id, len(id))
		}
	}
}

func TestEncodeOutputsKeepsSlotTypeAndAllowsAbsoluteFiles(t *testing.T) {
	workspace := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	inside := filepath.Join(workspace, "result.txt")
	if err := os.WriteFile(inside, []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := encodeOutput(Output{Seq: 1, Slot: "result", ContentType: "file", LocalPath: "result.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if values["content_type"] != "file" {
		t.Fatalf("workspace file outputs=%#v", values)
	}
	payload, _ := values["value"].(map[string]any)
	if payload["storage"] != "inline_base64" || payload["mime_type"] == nil {
		t.Fatalf("workspace file payload=%#v", payload)
	}

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "attachment.txt")
	if err := os.WriteFile(outside, []byte("tmp"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err = encodeOutput(Output{Seq: 1, Slot: "text_attachment", LocalPath: outside})
	if err != nil {
		t.Fatal(err)
	}
	if values["content_type"] != "file" {
		t.Fatalf("absolute file content_type=%v", values["content_type"])
	}

	parent := filepath.Join(filepath.Dir(workspace), "outside.txt")
	if err := os.WriteFile(parent, []byte("leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(parent) })
	if _, err := encodeOutput(Output{Seq: 1, Slot: "leak", LocalPath: filepath.Join("..", "outside.txt")}); err == nil {
		t.Fatal("relative path escaped the workspace")
	}

	values, err = encodeOutput(Output{Seq: 1, Slot: "image_attachment", ContentType: "image", Value: "https://placehold.co/640x360.png"})
	if err != nil {
		t.Fatal(err)
	}
	if values["content_type"] != "image" || values["value"] != "https://placehold.co/640x360.png" {
		t.Fatalf("image value outputs=%#v", values)
	}
}

func TestEncodeOutputRequiresStableSequenceAcrossCalls(t *testing.T) {
	if _, err := encodeOutput(Output{Slot: "pages", Value: "page"}); err == nil {
		t.Fatal("missing sequence accepted")
	}
	for i := 0; i < 2; i++ {
		value, err := encodeOutput(Output{Slot: "pages", Seq: 5, Value: "page"})
		if err != nil || value["seq"] != 5 {
			t.Fatalf("sequence changed on replay: %v %v", value, err)
		}
	}
}

func TestKeepHostGetFilesDropsGraphAndKeepsScripts(t *testing.T) {
	pkg := map[string]any{
		"workflow_id":    "image-workflow",
		"compiled_graph": map[string]any{"steps": []any{}},
		"files": map[string]any{
			"workflow.yaml":               base64.StdEncoding.EncodeToString([]byte("name: image")),
			"scripts/tools.py":            base64.StdEncoding.EncodeToString([]byte("def f():\n    pass\n")),
			"scripts/tests/test_tools.py": base64.StdEncoding.EncodeToString([]byte("assert True")),
			"scenario/driver.md":          base64.StdEncoding.EncodeToString([]byte("# driver")),
		},
	}
	keepHostGetFiles(pkg)
	if _, ok := pkg["compiled_graph"]; ok {
		t.Fatal("compiled_graph should be omitted")
	}
	files, _ := pkg["files"].(map[string]string)
	if len(files) != 1 || files["scripts/tools.py"] != "def f():\n    pass\n" {
		t.Fatalf("files=%#v", files)
	}
}
