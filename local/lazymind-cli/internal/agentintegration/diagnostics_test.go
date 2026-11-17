package agentintegration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectionLogReusesIdentifierAndRecordsChangedConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LAZYMIND_DESKTOP_APP_VERSION", "test-app-version")
	configured := true
	status := Status{Agent: "raccoon", State: Conflict, Enabled: &configured, Issue: &Issue{Code: "environment_mismatch"}}
	items := []Inspection{{Path: filepath.Join(root, "agent", "mcp.json"), Command: filepath.Join(root, "old", "lazymind"), Reason: "installation_mismatch"}}
	RecordInspection(root, filepath.Join(root, "current", "lazymind"), items, &status)
	id := status.DiagnosticID
	before, err := os.ReadFile(status.DiagnosticLog)
	if err != nil || id == "" {
		t.Fatal("missing diagnostic", err)
	}
	status = Status{Agent: "raccoon", State: Conflict, Enabled: &configured, Issue: &Issue{Code: "environment_mismatch"}}
	RecordInspection(root, filepath.Join(root, "current", "lazymind"), items, &status)
	after, _ := os.ReadFile(status.DiagnosticLog)
	if status.DiagnosticID != id || !bytes.Equal(before, after) {
		t.Fatal("unchanged poll duplicated the log")
	}
	if status.DiagnosticContext == nil || status.DiagnosticContext.Action != "inspect" ||
		status.DiagnosticContext.CurrentCLI != "~/current/lazymind" || status.DiagnosticContext.AppVersion != "test-app-version" ||
		len(status.DiagnosticContext.Configuration) != 1 || status.DiagnosticContext.Configuration[0].Command != "~/old/lazymind" ||
		status.Issue.OperationID != id || status.Issue.Stage != "inspect" {
		t.Fatalf("cached inspection lost current context: %+v", status)
	}
	if items[0].Command != filepath.Join(root, "old", "lazymind") {
		t.Fatal("diagnostic formatting mutated the actual inspection")
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

func TestOperationContextReportsCompletedDisconnectWithoutInventingRuntimeHealth(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	configured := false
	status := Status{Agent: "deepseek-harness", State: Ready, Enabled: &configured,
		Message: "raw output with private credentials must not enter diagnostic context"}
	operation := NewOperation(root, status.Agent, "disconnect", filepath.Join(root, "current", "lazymind"))
	operation.Stage = "disconnect"
	operation.After = []Inspection{{Path: filepath.Join(root, "dsh", "cordis.patch.yml")}}
	operation.Finish(&status)
	if status.DiagnosticContext.Action != "disconnect" || status.DiagnosticContext.Stage != "disconnect" ||
		status.Enabled == nil || *status.Enabled || status.Connection != nil || status.Issue != nil {
		t.Fatalf("disconnect evidence was lost or runtime state invented: %+v", status)
	}
	body, err := json.Marshal(status.DiagnosticContext)
	if err != nil || bytes.Contains(body, []byte(root)) || bytes.Contains(body, []byte("credentials")) {
		t.Fatalf("unsafe diagnostic context: %s, %v", body, err)
	}
}

func TestInspectionContextDoesNotConvertMissingConfigurationEvidenceToFalse(t *testing.T) {
	root := t.TempDir()
	status := Status{Agent: "deepseek-harness", State: Failed, Issue: &Issue{Code: "configuration_unreadable"}}
	items := []Inspection{{Path: filepath.Join(root, "cordis.patch.yml"), Reason: "permission_denied"}}
	RecordInspection(root, "/current/lazymind", items, &status)
	if status.Enabled != nil || status.Connection != nil || status.DiagnosticContext.Configuration[0].Reason != "permission_denied" {
		t.Fatalf("unreadable state became known or lost its reason: %+v", status)
	}
}
