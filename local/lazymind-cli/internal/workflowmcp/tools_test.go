package workflowmcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterBuildsEveryWorkflowToolSchema(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "schema-test", Version: "1"}, nil)
	Register(server, &Client{})
}

func TestReadOnlyClassificationCoversEveryWorkflowTool(t *testing.T) {
	readOnly := map[string]bool{
		"workflow.list": true, "workflow.get": true, "workflow.input.get": true,
		"workflow.state": true, "workflow.session.list": true,
		"workflow.artifact.list": true, "workflow.artifact.get": true,
	}
	if len(ToolNames) != 14 {
		t.Fatalf("tool count=%d, want 14", len(ToolNames))
	}
	for _, name := range ToolNames {
		if IsReadOnlyTool(name) != readOnly[name] {
			t.Fatalf("read-only classification for %s is %v", name, IsReadOnlyTool(name))
		}
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

func TestEncodeOutputsKeepsFilesInsideWorkspace(t *testing.T) {
	directory := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	path := filepath.Join(directory, "result.txt")
	if err := os.WriteFile(path, []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := encodeOutputs([]Output{{Slot: "result", LocalPath: path}})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0]["content_type"] != "text/plain; charset=utf-8" {
		t.Fatalf("outputs=%#v", values)
	}
	if _, err := encodeOutputs([]Output{{Slot: "leak", LocalPath: filepath.Join(directory, "..", "outside.txt")}}); err == nil {
		t.Fatal("outside-workspace path was accepted")
	}
}

func TestEncodeOutputsAssignsSequenceWithinEachSlot(t *testing.T) {
	values, err := encodeOutputs([]Output{
		{Slot: "items", Value: "first"},
		{Slot: "items", Value: "second"},
		{Slot: "summary", Value: "only"},
		{Slot: "items", Seq: 5, Value: "explicit"},
		{Slot: "items", Value: "after explicit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 2, 1, 5, 6}
	for index, value := range values {
		if value["seq"] != want[index] {
			t.Fatalf("output %d seq=%v, want %d", index, value["seq"], want[index])
		}
	}
}
