package subagent

import (
	"os"
	"testing"
)

// TestDBDSN returns env override or fallback to ACL_DB_DSN.
func TestDBDSN(t *testing.T) {
	// Save and restore env.
	orig := os.Getenv("LAZYMIND_SUBAGENT_DB_DSN")
	defer os.Setenv("LAZYMIND_SUBAGENT_DB_DSN", orig)

	// When LAZYMIND_SUBAGENT_DB_DSN is set, it takes priority.
	os.Setenv("LAZYMIND_SUBAGENT_DB_DSN", "postgres://custom")
	if got := DBDSN(); got != "postgres://custom" {
		t.Fatalf("got %q, want postgres://custom", got)
	}

	// When unset, falls back to ACL_DB_DSN.
	os.Unsetenv("LAZYMIND_SUBAGENT_DB_DSN")
	aclOrig := os.Getenv("ACL_DB_DSN")
	defer os.Setenv("ACL_DB_DSN", aclOrig)
	os.Setenv("ACL_DB_DSN", "postgres://acl")
	if got := DBDSN(); got != "postgres://acl" {
		t.Fatalf("got %q, want postgres://acl", got)
	}

	// Both unset returns empty (trimmed).
	os.Unsetenv("ACL_DB_DSN")
	if got := DBDSN(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

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
