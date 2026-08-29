package historyinjection

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverConversationWorkspacesSupportsNonPPTBuckets(t *testing.T) {
	uploadRoot := t.TempDir()
	conversationID := "conversation-image-1"
	workspace := filepath.Join(uploadRoot, "workflow-workspaces", "image-workflow", "owner-1",
		"animated_meme_sessions", conversationID)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := discoverConversationWorkspaces(ExportOptions{
		WorkflowRef: "builtin:image-workflow",
		UploadRoot:  uploadRoot,
	}, "owner-1", conversationID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/var/lib/lazymind/uploads/workflow-workspaces/image-workflow/owner-1/animated_meme_sessions/conversation-image-1"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("workspace paths = %#v, want %#v", paths, want)
	}
}

func TestDiscoverConversationWorkspacesAllowsMissingWorkflowRoot(t *testing.T) {
	paths, err := discoverConversationWorkspaces(ExportOptions{
		WorkflowRef: "builtin:image-workflow",
		UploadRoot:  t.TempDir(),
	}, "owner-1", "conversation-image-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("workspace paths = %#v, want none", paths)
	}
}

func TestExportSQLLiteralNormalizesSQLiteBooleanValues(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{
		{value: int64(1), want: "TRUE"},
		{value: int64(0), want: "FALSE"},
		{value: []byte("1"), want: "TRUE"},
		{value: "false", want: "FALSE"},
	} {
		if got := exportSQLLiteral(test.value, "plugin_slot_revisions", "selected", "owner", "admin"); got != test.want { // workflow-naming: persistence
			t.Fatalf("exportSQLLiteral(%#v) = %q, want %q", test.value, got, test.want)
		}
	}
	if got := exportSQLLiteral(int64(1), "plugin_slot_revisions", "revision", "owner", "admin"); got != "1" { // workflow-naming: persistence
		t.Fatalf("non-boolean integer = %q, want 1", got)
	}
	if got := exportSQLLiteral(int64(0), "task_center_tasks", "has_late_inputs", "owner", "admin"); got != "FALSE" {
		t.Fatalf("task center boolean = %q, want FALSE", got)
	}
}
