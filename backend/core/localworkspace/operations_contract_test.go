package localworkspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readWorkspaceSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		return ""
	}
	return string(body)
}

func TestWorkspaceOperationsExposeCoreFileActions(t *testing.T) {
	source := readWorkspaceSource(t, "operations.go")
	for _, symbol := range []string{"Read", "Create", "Append", "Replace", "Delete", "ExpectedVersion"} {
		if !strings.Contains(source, symbol) {
			t.Errorf("operations.go must expose %s for the workspace file contract", symbol)
		}
	}
}

func TestWorkspaceOperationsValidatePathAndVersionBeforeMutation(t *testing.T) {
	source := readWorkspaceSource(t, "operations.go")
	for _, guard := range []string{"filepath.Rel", "ResolveForConversation", "ExpectedVersion", "ModeSymlink", ".git", "os.Rename"} {
		if !strings.Contains(source, guard) {
			t.Errorf("operations.go is missing required mutation guard %q", guard)
		}
	}
}

func TestWorkspaceApprovalStateMachineHasSingleConsumeAndExpiry(t *testing.T) {
	source := readWorkspaceSource(t, "approvals.go") + readWorkspaceSource(t, "operations.go")
	for _, state := range []string{"pending", "allowed", "rejected", "expired", "executing", "completed", "uncertain"} {
		if !strings.Contains(source, state) {
			t.Errorf("approvals.go must represent state %q", state)
		}
	}
	for _, primitive := range []string{"SetNX", "CompareAndDelete", "operation_id"} {
		if !strings.Contains(source, primitive) {
			t.Errorf("approvals.go must use %s for bounded single-consume approval", primitive)
		}
	}
}
