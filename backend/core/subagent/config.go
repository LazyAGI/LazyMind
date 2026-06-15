package subagent

import (
	"os"
	"path/filepath"
	"strings"
)

// DBDSN returns the core database DSN (libpq key=value format) passed down to the
// algorithm layer so the SubAgent framework can persist steps and read/write artifacts.
// The Python side normalizes it via normalize_postgres_connection_url; it is never logged.
func DBDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("LAZYMIND_SUBAGENT_DB_DSN")); dsn != "" {
		return dsn
	}
	return strings.TrimSpace(os.Getenv("ACL_DB_DSN"))
}

// WorkspaceRoot is the base directory under which per-task workspaces live.
func WorkspaceRoot() string {
	if root := strings.TrimSpace(os.Getenv("LAZYMIND_SUBAGENT_WORKSPACE")); root != "" {
		return root
	}
	if root := strings.TrimSpace(os.Getenv("LAZYMIND_AGENTIC_WORKSPACE")); root != "" {
		return root
	}
	return "/data/subagent"
}

// WorkspacePath builds the per-task workspace path: <root>/<userID>/<taskID>/.
func WorkspacePath(userID, taskID string) string {
	user := strings.TrimSpace(userID)
	if user == "" {
		user = "anonymous"
	}
	return filepath.Join(WorkspaceRoot(), user, taskID) + string(os.PathSeparator)
}
