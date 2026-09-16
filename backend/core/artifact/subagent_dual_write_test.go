package artifact

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestDualWriteSubAgentCreatesImmutableMappedRevision(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := v2TestDB(t)
	svc := New(db.DB)
	task := SubAgentSnapshot{
		TaskID: "task-1", ConversationID: "conv-1", TriggerHistoryID: "history-1",
		OwnerUserID: "user-1", WorkspacePath: t.TempDir(), AgentType: "research",
	}
	row := SubAgentLegacyArtifact{
		ID: "saa-1", Slot: "report", ContentType: "text", Seq: 7,
		Value: json.RawMessage(`{"text":"done"}`),
	}
	view, err := DualWriteSubAgent(context.Background(), svc, task, row)
	if err != nil {
		t.Fatal(err)
	}
	if view.LogicalKey != "subagent/task-1/saa-1" || view.ProducerType != ProducerSubAgent {
		t.Fatalf("view=%+v", view)
	}
	var bindings []orm.ArtifactBinding
	if err := db.Where("artifact_id = ?", view.ArtifactID).Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		ScopeTask + ":task-1": true, ScopeConversation + ":conv-1": true,
		ScopeHistory + ":history-1": true, ScopeSubAgentLegacyRow + ":saa-1": true,
	}
	for _, binding := range bindings {
		delete(want, binding.ScopeType+":"+binding.ScopeID)
	}
	if len(want) != 0 {
		t.Fatalf("missing bindings=%v", want)
	}
}

func TestDualWriteSubAgentSnapshotsFileAndRejectsOutsideWorkspace(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := v2TestDB(t)
	svc := New(db.DB)
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(inside, []byte("private report"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := SubAgentSnapshot{TaskID: "task-file", OwnerUserID: "user-1", WorkspacePath: workspace, AgentType: "research"}
	row := SubAgentLegacyArtifact{ID: "saa-file", Slot: "report", ContentType: "file", Value: json.RawMessage(`{"path":"report.txt","filename":"report.txt"}`)}
	view, err := DualWriteSubAgent(context.Background(), svc, task, row)
	if err != nil {
		t.Fatal(err)
	}
	var revision orm.ArtifactRevision
	if err := db.Where("id = ?", view.RevisionID).Take(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if revision.BlobID == "" || strings.Contains(string(revision.Metadata), workspace) {
		t.Fatalf("revision=%+v", revision)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	row.ID, row.Value = "saa-outside", json.RawMessage(`{"path":"`+outside+`"}`)
	if _, err := DualWriteSubAgent(context.Background(), svc, task, row); err != ErrAccessDenied {
		t.Fatalf("err=%v", err)
	}
	link := filepath.Join(workspace, "linked.txt")
	if err := os.Symlink(inside, link); err != nil {
		t.Fatal(err)
	}
	row.ID, row.Value = "saa-link", json.RawMessage(`{"path":"linked.txt"}`)
	if _, err := DualWriteSubAgent(context.Background(), svc, task, row); err != ErrAccessDenied {
		t.Fatalf("symlink err=%v", err)
	}
}

func TestDualWriteSubAgentReplayIsIdempotent(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	task := SubAgentSnapshot{TaskID: "task-1", OwnerUserID: "user-1", AgentType: "research"}
	row := SubAgentLegacyArtifact{ID: "saa-1", Slot: "result", ContentType: "json", Value: json.RawMessage(`{"ok":true}`)}
	first, err := DualWriteSubAgent(context.Background(), svc, task, row)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DualWriteSubAgent(context.Background(), svc, task, row)
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionID != again.RevisionID {
		t.Fatalf("first=%s again=%s", first.RevisionID, again.RevisionID)
	}
}
