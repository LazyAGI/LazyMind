package agentintegration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectionLogReusesIdentifierAndRecordsChangedConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	configured := true
	status := Status{Agent: "raccoon", State: Conflict, Enabled: &configured, Issue: &Issue{Code: "environment_mismatch"}}
	items := []Inspection{{Path: filepath.Join(root, "agent", "mcp.json"), Command: filepath.Join(root, "old", "lazymind"), Reason: "installation_mismatch"}}
	RecordInspection(root, filepath.Join(root, "current", "lazymind"), items, &status)
	id := status.DiagnosticID
	before, err := os.ReadFile(status.DiagnosticLog)
	if err != nil || id == "" {
		t.Fatal("missing diagnostic", err)
	}
	RecordInspection(root, filepath.Join(root, "current", "lazymind"), items, &status)
	after, _ := os.ReadFile(status.DiagnosticLog)
	if status.DiagnosticID != id || !bytes.Equal(before, after) {
		t.Fatal("unchanged poll duplicated the log")
	}
	items[0].Command = filepath.Join(root, "new", "lazymind")
	RecordInspection(root, filepath.Join(root, "current", "lazymind"), items, &status)
	after, _ = os.ReadFile(status.DiagnosticLog)
	if status.DiagnosticID == id || bytes.Count(after, []byte("\n")) != 2 {
		t.Fatal("changed environment was not recorded")
	}
	if bytes.Contains(after, []byte(root)) {
		t.Fatal("user directory was not shortened")
	}
}
