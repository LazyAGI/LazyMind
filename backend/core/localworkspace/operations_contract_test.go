package localworkspace

import (
	"os"
	"path/filepath"
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
