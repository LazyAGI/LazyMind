package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportRegisteredOpenAPIToWritesOnlyRequestedWorkspaceSpec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.yaml")
	if err := exportRegisteredOpenAPITo(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"/api/core/local-workspaces:", "/api/core/local-workspaces/{workspace_id}:revoke:", "/api/core/conversations/{conversation_id}:workspace:", "workspace-permission", "permission_mode"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
}
