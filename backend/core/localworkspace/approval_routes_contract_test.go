package localworkspace

import (
	"os"
	"strings"
	"testing"
)

func TestWorkspaceOperationRoutesAreRegisteredWithInternalAndUserAuth(t *testing.T) {
	body, err := os.ReadFile("../routes.go")
	if err != nil {
		t.Errorf("read routes.go: %v", err)
		return
	}
	source := string(body)
	for _, route := range []string{
		"workspace-operations:prepare",
		"workspace-operations/{operation_id}",
		"workspace-operations/{operation_id}:execute",
		"workspace-operations/{operation_id}:claim",
		"workspace-operations/{operation_id}:complete",
		"workspace-approvals/{operation_id}:decide",
	} {
		if !strings.Contains(source, route) {
			t.Errorf("routes.go is missing workspace operation route %q", route)
		}
	}
}
