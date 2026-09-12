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
