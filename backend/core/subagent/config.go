package subagent

import (
	"os"
	"path/filepath"
	"strings"
)

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
