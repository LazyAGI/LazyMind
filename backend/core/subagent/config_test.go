package subagent

import (
	"os"
	"testing"
)

// TestWorkspaceRoot returns env override or default.
func TestWorkspaceRoot(t *testing.T) {
	orig := os.Getenv("LAZYMIND_SUBAGENT_WORKSPACE")
	defer os.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", orig)

	os.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", "/custom/workspace")
	if got := WorkspaceRoot(); got != "/custom/workspace" {
		t.Fatalf("got %q, want /custom/workspace", got)
	}

	os.Unsetenv("LAZYMIND_SUBAGENT_WORKSPACE")
	// Default.
	if got := WorkspaceRoot(); got != "/data/subagent" {
		t.Fatalf("got %q, want /data/subagent", got)
	}
}

// TestWorkspacePath builds path with user and task IDs.
func TestWorkspacePath(t *testing.T) {
	got := WorkspacePath("user1", "task-1")
	if got != "/data/subagent/user1/task-1/" {
		t.Fatalf("got %q", got)
	}
	// Empty userID defaults to anonymous.
	got2 := WorkspacePath("", "task-2")
	if got2 != "/data/subagent/anonymous/task-2/" {
		t.Fatalf("got %q", got2)
	}
	// Trim whitespace.
	got3 := WorkspacePath("  u1  ", "t1")
	if got3 != "/data/subagent/u1/t1/" {
		t.Fatalf("got %q", got3)
	}
}
