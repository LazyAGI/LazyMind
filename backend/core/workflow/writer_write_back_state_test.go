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

func TestEnrichWriterWriteBackSlots_UsesSourceDocumentAfterHumanEdit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)

	sourcePath := filepath.Join(root, "source_document.lmd")
	syncedPath := filepath.Join(root, "synced_document.lmd")
	mustWriteWriterTestArtifact(t, sourcePath, `{
  "data": {
    "document_id": "draft-document-session-1",
    "provider_binding": {
      "provider": "feishu",
      "document_id": "DP02daLTAopqQcxO094cQ928ngg",
      "uri": "https://jcnufyovyxrs.feishu.cn/docx/DP02daLTAopqQcxO094cQ928ngg"
    }
  }
}`)
	mustWriteWriterTestArtifact(t, syncedPath, `{
  "data": {"document_id": "draft-document-session-1"},
  "meta": {"lazymind_provider_sync": {"confirmed": true}}
}`)

	now := time.Now().UTC()
	for _, step := range []orm.WorkflowSessionStep{
		{ID: "prepare-step", SessionID: "session-1", StepID: "prepare", Attempt: 1, TaskID: "prepare-task", Status: StepStatusSucceeded, Validity: "effective", CreatedAt: now, UpdatedAt: now},
		{ID: "write-step", SessionID: "session-1", StepID: "write_document", Attempt: 1, TaskID: "write-task", Status: StepStatusSucceeded, Validity: "effective", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.DB.Create(&step).Error; err != nil {
			t.Fatalf("create step: %v", err)
		}
	}
	if err := db.DB.Create(&orm.SubAgentArtifact{
		ID: "source-artifact", TaskID: "prepare-task", Slot: "source_document", ContentType: "file",
		Value: writerTestPathValue(sourcePath), Seq: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create source artifact: %v", err)
	}
	if err := db.DB.Create(&orm.SubAgentArtifact{
		ID: "synced-artifact", TaskID: "write-task", Slot: "draft_document", ContentType: "file",
		Value: writerTestPathValue(syncedPath), Seq: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create synced artifact: %v", err)
	}

	seq := 1
	sourceRevision := orm.WorkflowSlotRevision{
		ID: "source-rev", SessionID: "session-1", SlotID: "source_document", Revision: 1, Selected: true,
		ArtifactSeq: &seq, ChangeSource: "ai", Slot: "source_document", StepID: "prepare", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	syncedRevision := orm.WorkflowSlotRevision{
		ID: "synced-rev", SessionID: "session-1", SlotID: "draft_document", Revision: 1, Selected: false,
		ArtifactSeq: &seq, ChangeSource: "ai", Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	humanRevision := orm.WorkflowSlotRevision{
		ID: "human-rev", SessionID: "session-1", SlotID: "draft_document", Revision: 2, Selected: true,
		ContentSnapshot: json.RawMessage(`{"data":{"document_id":"draft-document-session-1"}}`),
		ChangeSource:    "human", Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	for _, revision := range []orm.WorkflowSlotRevision{sourceRevision, syncedRevision, humanRevision} {
		if err := db.DB.Create(&revision).Error; err != nil {
			t.Fatalf("create revision: %v", err)
		}
	}

	slots := []slotDTO{toSlotDTO(&sourceRevision), toSlotDTO(&humanRevision)}
	enrichSlots(ctx, db.DB, "session-1", slots)
	draft := slots[1]
	if !draft.WriteBackReady || !draft.WriteBackDirty || draft.WriteBackState != writerWriteBackSyncedDirty {
		t.Fatalf("unexpected write-back state: ready=%t dirty=%t state=%q", draft.WriteBackReady, draft.WriteBackDirty, draft.WriteBackState)
	}
	if got, want := draft.WriteBackURL, "https://jcnufyovyxrs.feishu.cn/docx/DP02daLTAopqQcxO094cQ928ngg"; got != want {
		t.Fatalf("write-back URL = %q, want %q", got, want)
	}
	if draft.Provider != "feishu" || draft.ProviderDocumentID != "DP02daLTAopqQcxO094cQ928ngg" {
		t.Fatalf("unexpected provider identity: provider=%q document_id=%q", draft.Provider, draft.ProviderDocumentID)
	}
	if draft.LastSyncedRevision == nil || *draft.LastSyncedRevision != 1 {
		t.Fatalf("last_synced_revision = %v, want 1", draft.LastSyncedRevision)
	}
}

func TestEnrichWriterWriteBackSlots_ExposesInitialDelivery(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)

	sourcePath := filepath.Join(root, "source_document.lmd")
	mustWriteWriterTestArtifact(t, sourcePath, `{
  "data": {
    "document_id": "draft-document-session-2",
    "provider_binding": {
      "provider": "feishu",
      "document_id": "DP02daLTAopqQcxO094cQ928ngg",
      "uri": "https://jcnufyovyxrs.feishu.cn/docx/DP02daLTAopqQcxO094cQ928ngg"
    }
  }
}`)

	now := time.Now().UTC()
	step := orm.WorkflowSessionStep{ID: "prepare-step", SessionID: "session-2", StepID: "prepare", Attempt: 1, TaskID: "prepare-task", Status: StepStatusSucceeded, Validity: "effective", CreatedAt: now, UpdatedAt: now}
	if err := db.DB.Create(&step).Error; err != nil {
		t.Fatalf("create step: %v", err)
	}
	if err := db.DB.Create(&orm.SubAgentArtifact{
		ID: "source-artifact", TaskID: "prepare-task", Slot: "source_document", ContentType: "file",
		Value: writerTestPathValue(sourcePath), Seq: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create source artifact: %v", err)
	}
	seq := 1
	sourceRevision := orm.WorkflowSlotRevision{
		ID: "source-rev", SessionID: "session-2", SlotID: "source_document", Revision: 1, Selected: true,
		ArtifactSeq: &seq, ChangeSource: "ai", Slot: "source_document", StepID: "prepare", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	draftRevision := orm.WorkflowSlotRevision{
		ID: "draft-rev", SessionID: "session-2", SlotID: "draft_document", Revision: 1, Selected: true,
		ContentSnapshot: json.RawMessage(`{"data":{"document_id":"draft-document-session-2"}}`),
		ChangeSource:    "ai", Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	for _, revision := range []orm.WorkflowSlotRevision{sourceRevision, draftRevision} {
		if err := db.DB.Create(&revision).Error; err != nil {
			t.Fatalf("create revision: %v", err)
		}
	}

	slots := []slotDTO{toSlotDTO(&sourceRevision), toSlotDTO(&draftRevision)}
	enrichSlots(ctx, db.DB, "session-2", slots)
	draft := slots[1]
	if !draft.WriteBackReady || !draft.WriteBackDirty || draft.WriteBackState != writerWriteBackInitialDelivery {
		t.Fatalf("unexpected write-back state: ready=%t dirty=%t state=%q", draft.WriteBackReady, draft.WriteBackDirty, draft.WriteBackState)
	}
	if draft.LastSyncedRevision != nil {
		t.Fatalf("last_synced_revision = %v, want nil", *draft.LastSyncedRevision)
	}
}

func TestEnrichWriterWriteBackSlots_UsesCurrentSyncedRevision(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	source := orm.WorkflowSlotRevision{
		ID: "source-rev", SessionID: "session-3", SlotID: "source_document", Revision: 1, Selected: true,
		ContentSnapshot: json.RawMessage(`{"data":{"provider_binding":{"provider":"feishu","document_id":"doc-3","uri":"https://tenant.feishu.cn/docx/doc-3"}}}`),
		ChangeSource:    "ai", Slot: "source_document", StepID: "prepare", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	draft := orm.WorkflowSlotRevision{
		ID: "draft-rev", SessionID: "session-3", SlotID: "draft_document", Revision: 4, Selected: true,
		ContentSnapshot: json.RawMessage(`{"data":{"document_id":"draft-document-session-3"}}`),
		ChangeSource:    "provider_sync", Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	for _, revision := range []orm.WorkflowSlotRevision{source, draft} {
		if err := db.DB.Create(&revision).Error; err != nil {
			t.Fatalf("create revision: %v", err)
		}
	}
	slots := []slotDTO{toSlotDTO(&source), toSlotDTO(&draft)}
	enrichSlots(ctx, db.DB, "session-3", slots)
	got := slots[1]
	if got.WriteBackState != writerWriteBackSyncedClean || got.LastSyncedRevision == nil || *got.LastSyncedRevision != 4 {
		t.Fatalf("unexpected synced state: state=%q last_synced_revision=%v", got.WriteBackState, got.LastSyncedRevision)
	}
}

func TestEnrichWriterWriteBackSlots_BlocksWithoutSourceDocument(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	draft := orm.WorkflowSlotRevision{
		ID: "draft-rev", SessionID: "session-4", SlotID: "draft_document", Revision: 1, Selected: true,
		ContentSnapshot: json.RawMessage(`{"data":{"document_id":"draft-document-session-4"}}`),
		ChangeSource:    "human", Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	if err := db.DB.Create(&draft).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}
	slots := []slotDTO{toSlotDTO(&draft)}
	enrichSlots(ctx, db.DB, "session-4", slots)
	got := slots[0]
	if got.WriteBackReady || got.WriteBackState != writerWriteBackBlocked || got.Provider != "" || got.ProviderDocumentID != "" {
		t.Fatalf("unexpected blocked state: ready=%t state=%q provider=%q document_id=%q", got.WriteBackReady, got.WriteBackState, got.Provider, got.ProviderDocumentID)
	}
}

func TestEnrichWriterWriteBackSlots_ExposesInitialDeliveryForMarkdownWithoutSource(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)

	draftPath := filepath.Join(root, "ordinary-writing.md")
	mustWriteWriterTestArtifact(t, draftPath, "# Ordinary writing\n\nReady for Feishu.\n")
	now := time.Now().UTC()
	draft := orm.WorkflowSlotRevision{
		ID: "draft-rev", SessionID: "session-5", SlotID: "draft_document", Revision: 1, Selected: true,
		ContentSnapshot: writerTestPathValue(draftPath),
		ChangeSource:    "ai", Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	if err := db.DB.Create(&draft).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}

	slots := []slotDTO{toSlotDTO(&draft)}
	enrichSlots(ctx, db.DB, "session-5", slots)
	got := slots[0]
	if !got.WriteBackReady || !got.WriteBackDirty || got.WriteBackState != writerWriteBackInitialDelivery {
		t.Fatalf("unexpected initial delivery state: ready=%t dirty=%t state=%q", got.WriteBackReady, got.WriteBackDirty, got.WriteBackState)
	}
	if got.WriteBackURL != "" || got.Provider != "" || got.ProviderDocumentID != "" || got.LastSyncedRevision != nil {
		t.Fatalf("unexpected target before first delivery: url=%q provider=%q document_id=%q last_synced=%v", got.WriteBackURL, got.Provider, got.ProviderDocumentID, got.LastSyncedRevision)
	}
}

func TestEnrichWriterWriteBackSlots_UsesCreatedDocumentBindingWithoutSource(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	draft := orm.WorkflowSlotRevision{
		ID: "draft-rev", SessionID: "session-6", SlotID: "draft_document", Revision: 2, Selected: true,
		ContentSnapshot: json.RawMessage(`{
  "data": {
    "document_id": "draft-document-session-6",
    "provider_binding": {
      "provider": "feishu",
      "document_id": "doc-created-from-markdown",
      "uri": "https://tenant.feishu.cn/docx/doc-created-from-markdown"
    }
  }
}`),
		ChangeSource: "provider_sync", Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}
	if err := db.DB.Create(&draft).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}

	slots := []slotDTO{toSlotDTO(&draft)}
	enrichSlots(ctx, db.DB, "session-6", slots)
	got := slots[0]
	if !got.WriteBackReady || got.WriteBackDirty || got.WriteBackState != writerWriteBackSyncedClean {
		t.Fatalf("unexpected synced state: ready=%t dirty=%t state=%q", got.WriteBackReady, got.WriteBackDirty, got.WriteBackState)
	}
	if got.WriteBackURL != "https://tenant.feishu.cn/docx/doc-created-from-markdown" || got.ProviderDocumentID != "doc-created-from-markdown" {
		t.Fatalf("unexpected created target: url=%q document_id=%q", got.WriteBackURL, got.ProviderDocumentID)
	}
	if got.LastSyncedRevision == nil || *got.LastSyncedRevision != 2 {
		t.Fatalf("last_synced_revision = %v, want 2", got.LastSyncedRevision)
	}
}

func writerTestPathValue(path string) json.RawMessage {
	value, _ := json.Marshal(map[string]string{"path": path, "filename": filepath.Base(path)})
	return value
}

func mustWriteWriterTestArtifact(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
