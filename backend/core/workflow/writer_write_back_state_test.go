package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestEnrichWriterWriteBackSlots_UsesSourceAndLatestSync(t *testing.T) {
	db := newTestDB(t)
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	sourcePath := filepath.Join(root, "source_document.lmd")
	syncedPath := filepath.Join(root, "draft_document.lmd")
	mustWriteWriterArtifact(t, sourcePath, `{"data":{"document_id":"draft-1","provider_binding":{"provider":"feishu","document_id":"doc-1","uri":"https://tenant.feishu.cn/docx/doc-1"}}}`)
	mustWriteWriterArtifact(t, syncedPath, `{"data":{"document_id":"draft-1"},"meta":{"lazymind_provider_sync":{"confirmed":true}}}`)

	now := time.Now().UTC()
	for _, step := range []orm.WorkflowSessionStep{
		{ID: "prepare", SessionID: "session", StepID: "prepare", Attempt: 1, TaskID: "prepare-task", Status: StepStatusSucceeded, Validity: "effective", CreatedAt: now, UpdatedAt: now},
		{ID: "write", SessionID: "session", StepID: "write_document", Attempt: 1, TaskID: "write-task", Status: StepStatusSucceeded, Validity: "effective", CreatedAt: now, UpdatedAt: now},
	} {
		mustCreateWriterRecord(t, db.DB.Create(&step).Error)
	}
	for _, artifact := range []orm.SubAgentArtifact{
		{ID: "source", TaskID: "prepare-task", Slot: "source_document", ContentType: "file", Value: writerPathValue(sourcePath), Seq: 1, CreatedAt: now},
		{ID: "synced", TaskID: "write-task", Slot: "draft_document", ContentType: "file", Value: writerPathValue(syncedPath), Seq: 1, CreatedAt: now},
	} {
		mustCreateWriterRecord(t, db.DB.Create(&artifact).Error)
	}

	seq := 1
	source := writerRevision("source-rev", "session", "source_document", 1, "ai", nil)
	source.ArtifactSeq, source.StepID = &seq, "prepare"
	synced := writerRevision("synced-rev", "session", "draft_document", 1, "host", nil)
	synced.Selected, synced.ArtifactSeq = false, &seq
	human := writerRevision("human-rev", "session", "draft_document", 2, "human", json.RawMessage(`{"data":{"document_id":"draft-1"}}`))
	for _, revision := range []*orm.WorkflowSlotRevision{&source, &synced, &human} {
		mustCreateWriterRecord(t, db.DB.Create(revision).Error)
	}

	slots := []slotDTO{toSlotDTO(&source), toSlotDTO(&human)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	draft := slots[1]
	if !draft.WriteBackReady || !draft.WriteBackDirty || draft.WriteBackState != writerWriteBackSyncedDirty || draft.ProviderDocumentID != "doc-1" {
		t.Fatalf("unexpected write-back state: %+v", draft)
	}
	if draft.LastSyncedRevision == nil || *draft.LastSyncedRevision != 1 {
		t.Fatalf("last_synced_revision = %v, want 1", draft.LastSyncedRevision)
	}
}

func TestEnrichWriterWriteBackSlots_MarkdownInitialDelivery(t *testing.T) {
	db := newTestDB(t)
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	draftPath := filepath.Join(root, "draft_document.md")
	mustWriteWriterArtifact(t, draftPath, "# Ready for Feishu\n")
	draft := writerRevision("draft", "session", "draft_document", 1, "ai", writerPathValue(draftPath))
	mustCreateWriterRecord(t, db.DB.Create(&draft).Error)

	slots := []slotDTO{toSlotDTO(&draft)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	got := slots[0]
	if !got.WriteBackReady || !got.WriteBackDirty || got.WriteBackState != writerWriteBackInitialDelivery || got.ProviderDocumentID != "" {
		t.Fatalf("unexpected initial delivery state: %+v", got)
	}
}

func TestEnrichWriterWriteBackSlots_InlineMarkdownInitialDelivery(t *testing.T) {
	db := newTestDB(t)
	draft := writerRevision("draft", "session", "draft_document", 1, "human", json.RawMessage(
		`{"schema":"text/markdown","data":"# Ready for Feishu\n"}`,
	))
	mustCreateWriterRecord(t, db.DB.Create(&draft).Error)

	slots := []slotDTO{toSlotDTO(&draft)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	got := slots[0]
	if !got.WriteBackReady || !got.WriteBackDirty || got.WriteBackState != writerWriteBackInitialDelivery {
		t.Fatalf("unexpected inline Markdown delivery state: %+v", got)
	}
}

func TestEnrichWriterWriteBackSlots_LocalIRInitialDelivery(t *testing.T) {
	db := newTestDB(t)
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	sourcePath := filepath.Join(root, "source_document.lmd")
	draftPath := filepath.Join(root, "draft_document.lmd")
	document := `{"data":{"document_id":"local-doc","stage":"final","blocks":[],"provider_binding":{}}}`
	mustWriteWriterArtifact(t, sourcePath, document)
	mustWriteWriterArtifact(t, draftPath, document)
	source := writerRevision("source", "session", "source_document", 1, "ai", writerPathValue(sourcePath))
	draft := writerRevision("draft", "session", "draft_document", 1, "ai", writerPathValue(draftPath))
	mustCreateWriterRecord(t, db.DB.Create(&source).Error)
	mustCreateWriterRecord(t, db.DB.Create(&draft).Error)

	slots := []slotDTO{toSlotDTO(&source), toSlotDTO(&draft)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	got := slots[1]
	if !got.WriteBackReady || !got.WriteBackDirty || got.WriteBackState != writerWriteBackInitialDelivery {
		t.Fatalf("unexpected local IR delivery state: %+v", got)
	}
}

func TestEnrichWriterWriteBackSlots_ProviderSyncIsClean(t *testing.T) {
	db := newTestDB(t)
	draft := writerRevision("draft", "session", "draft_document", 2, "provider_sync", json.RawMessage(`{"data":{"provider_binding":{"provider":"feishu","document_id":"doc-2","uri":"https://tenant.feishu.cn/docx/doc-2"}}}`))
	mustCreateWriterRecord(t, db.DB.Create(&draft).Error)

	slots := []slotDTO{toSlotDTO(&draft)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	got := slots[0]
	if !got.WriteBackReady || got.WriteBackDirty || got.WriteBackState != writerWriteBackSyncedClean || got.LastSyncedRevision == nil || *got.LastSyncedRevision != 2 {
		t.Fatalf("unexpected synced state: %+v", got)
	}
}

func writerRevision(id, sessionID, slot string, revision int, source string, content json.RawMessage) orm.WorkflowSlotRevision {
	return orm.WorkflowSlotRevision{
		ID: id, SessionID: sessionID, SlotID: slot, Revision: revision, Selected: true,
		ContentSnapshot: content, ChangeSource: source, Slot: slot, StepID: "write_document",
		Attempt: 1, Validity: "effective", CreatedAt: time.Now().UTC(),
	}
}

func writerPathValue(path string) json.RawMessage {
	value, _ := json.Marshal(map[string]string{"path": path, "filename": filepath.Base(path)})
	return value
}

func mustWriteWriterArtifact(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustCreateWriterRecord(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
