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
	mustCreateWriterRecord(t, db.DB.Create(&orm.WorkflowSession{
		ID: "session", WorkflowID: "writer-workflow",
	}).Error)
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
	if slots[0].EditorProfile != "" {
		t.Fatalf("non-GitHub source editor profile = %q, want empty", slots[0].EditorProfile)
	}
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

func TestEnrichWriterWriteBackSlots_UsesGitHubTarget(t *testing.T) {
	db := newTestDB(t)
	mustCreateWriterRecord(t, db.DB.Create(&orm.WorkflowSession{
		ID: "session", WorkflowID: "writer-workflow",
	}).Error)
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	sourcePath := filepath.Join(root, "source_document.md")
	draftPath := filepath.Join(root, "draft_document.md")
	targetPath := filepath.Join(root, "target_document.json")
	mustWriteWriterArtifact(t, sourcePath, "# Imported from GitHub\n")
	mustWriteWriterArtifact(t, draftPath, "# Ready for GitHub\n")
	mustWriteWriterArtifact(t, targetPath, `{"data":{"doc_id":"acme/docs:main:README.md","adapter":"github","uri":"githubrepo:/acme/docs/README.md?ref=main","meta":{"browser_url":"https://github.com/acme/docs/blob/main/README.md"}}}`)
	source := writerRevision("source", "session", "source_document", 1, "host", writerPathValue(sourcePath))
	draft := writerRevision("draft", "session", "draft_document", 1, "ai", writerPathValue(draftPath))
	target := writerRevision("target", "session", "target_document", 1, "ai", writerPathValue(targetPath))
	mustCreateWriterRecord(t, db.DB.Create(&source).Error)
	mustCreateWriterRecord(t, db.DB.Create(&draft).Error)
	mustCreateWriterRecord(t, db.DB.Create(&target).Error)

	slots := []slotDTO{toSlotDTO(&source), toSlotDTO(&target), toSlotDTO(&draft)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	if slots[0].EditorProfile != writerMarkdownSourceEditor {
		t.Fatalf("source editor profile = %q, want %q", slots[0].EditorProfile, writerMarkdownSourceEditor)
	}
	got := slots[2]
	if got.Provider != "github" || got.ProviderDocumentID != "acme/docs:main:README.md" || !got.WriteBackReady {
		t.Fatalf("unexpected GitHub write-back state: %+v", got)
	}
	mustCreateWriterRecord(t, db.DB.Model(&orm.WorkflowSession{}).
		Where("id = ?", "session").Update("plugin_id", "other-workflow").Error)
	otherWorkflowSlots := []slotDTO{toSlotDTO(&source), toSlotDTO(&target)}
	enrichSlots(context.Background(), db.DB, "session", otherWorkflowSlots)
	if otherWorkflowSlots[0].EditorProfile != "" {
		t.Fatalf("other workflow editor profile = %q, want empty", otherWorkflowSlots[0].EditorProfile)
	}
}

func TestEnrichWriterWriteBackSlots_CurrentTargetSurvivesEditorCheckpoint(t *testing.T) {
	db := newTestDB(t)
	mustCreateWriterRecord(t, db.AutoMigrate(&orm.WorkflowHumanArtifact{}))
	mustCreateWriterRecord(t, db.Create(&orm.WorkflowSession{ID: "session", WorkflowID: "writer-workflow"}).Error)
	mustCreateWriterRecord(t, db.Create(&orm.WorkflowHumanArtifact{
		ID: "edited", SessionID: "session", Slot: "draft_document", ContentType: "text/markdown",
		Value: json.RawMessage(`{"text":"# Edited draft"}`),
	}).Error)
	draft := writerRevision("draft", "session", "draft_document", 3, "human", nil)
	humanID := "edited"
	draft.HumanArtifactID = &humanID
	target := writerRevision("target", "session", "target_document", 2, "provider_sync",
		json.RawMessage(`{"data":{"adapter":"github","doc_id":"acme/docs:branch:README.md","meta":{"pull_request_url":"https://github.com/acme/docs/pull/7"}}}`))
	old := writerRevision("old-target", "session", "target_document", 1, "host",
		json.RawMessage(`{"data":{"adapter":"github","doc_id":"acme/docs:main:README.md","meta":{"browser_url":"https://github.com/acme/docs/blob/main/README.md"}}}`))
	old.Selected = false
	for _, revision := range []*orm.WorkflowSlotRevision{&draft, &target, &old} {
		mustCreateWriterRecord(t, db.Create(revision).Error)
	}
	old.Selected = false
	mustCreateWriterRecord(t, db.Model(&old).Update("selected", false).Error)
	// Session reads append historical step outputs after the selected artifacts.
	slots := []slotDTO{toSlotDTO(&draft), toSlotDTO(&target), toSlotDTO(&old)}
	enrichSlots(t.Context(), db.DB, "session", slots)
	got := slots[0]
	if got.Provider != "github" || !got.WriteBackReady || got.ProviderDocumentID != "acme/docs:branch:README.md" ||
		got.WriteBackURL != "https://github.com/acme/docs/pull/7" {
		t.Fatalf("checkpoint lost its current publication target: %+v", got)
	}
}

func TestEnrichWriterWriteBackSlots_UsesObsidianTarget(t *testing.T) {
	db := newTestDB(t)
	mustCreateWriterRecord(t, db.DB.Create(&orm.WorkflowSession{
		ID: "session", WorkflowID: "writer-workflow",
	}).Error)
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	sourcePath := filepath.Join(root, "source_document.md")
	targetPath := filepath.Join(root, "target_document.json")
	mustWriteWriterArtifact(t, sourcePath, "# Imported from Obsidian\n")
	mustWriteWriterArtifact(t, targetPath, `{"data":{"doc_id":"vlt_1:note.md","adapter":"obsidian","uri":"obsidian://vlt_1/note.md","meta":{"local_path":"/mnt/obsidian/obs/note.md"}}}`)
	source := writerRevision("source", "session", "source_document", 1, "host", writerPathValue(sourcePath))
	target := writerRevision("target", "session", "target_document", 1, "host", writerPathValue(targetPath))
	mustCreateWriterRecord(t, db.DB.Create(&source).Error)
	mustCreateWriterRecord(t, db.DB.Create(&target).Error)

	slots := []slotDTO{toSlotDTO(&source), toSlotDTO(&target)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	if slots[0].EditorProfile != writerMarkdownSourceEditor {
		t.Fatalf("source editor profile = %q, want %q", slots[0].EditorProfile, writerMarkdownSourceEditor)
	}
}

func TestEnrichWriterWriteBackSlots_DoesNotUseMarkdownProfileForIRSource(t *testing.T) {
	for _, provider := range []struct {
		name    string
		adapter string
		uri     string
	}{
		{name: "Feishu", adapter: "feishu", uri: "https://tenant.feishu.cn/docx/doc-1"},
		{name: "Notion", adapter: "notion", uri: "https://app.notion.com/p/page-1"},
	} {
		t.Run(provider.name, func(t *testing.T) {
			db := newTestDB(t)
			mustCreateWriterRecord(t, db.DB.Create(&orm.WorkflowSession{
				ID: "session", WorkflowID: "writer-workflow",
			}).Error)
			root := t.TempDir()
			t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
			sourcePath := filepath.Join(root, "source_document.lmd")
			targetPath := filepath.Join(root, "target_document.json")
			mustWriteWriterArtifact(t, sourcePath, `{"data":{"document_id":"doc-1","blocks":[]}}`)
			mustWriteWriterArtifact(t, targetPath, `{"data":{"doc_id":"doc-1","adapter":"`+provider.adapter+`","uri":"`+provider.uri+`"}}`)
			source := writerRevision("source", "session", "source_document", 1, "host", writerPathValue(sourcePath))
			target := writerRevision("target", "session", "target_document", 1, "host", writerPathValue(targetPath))
			mustCreateWriterRecord(t, db.DB.Create(&source).Error)
			mustCreateWriterRecord(t, db.DB.Create(&target).Error)

			slots := []slotDTO{toSlotDTO(&source), toSlotDTO(&target)}
			enrichSlots(context.Background(), db.DB, "session", slots)
			if slots[0].EditorProfile != "" {
				t.Fatalf("source editor profile = %q, want empty for IR source", slots[0].EditorProfile)
			}
		})
	}
}

func TestEnrichWriterWriteBackSlots_InlineMarkdownInitialDelivery(t *testing.T) {
	db := newTestDB(t)
	draft := writerRevision("draft", "session", "flat_draft_document", 1, "human", json.RawMessage(
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

func TestEnrichWriterWriteBackSlots_NotionProviderSyncIsClean(t *testing.T) {
	db := newTestDB(t)
	draft := writerRevision("draft", "session", "draft_document", 2, "provider_sync", json.RawMessage(`{"data":{"provider_binding":{"provider":"notion","document_id":"page-2","uri":"https://app.notion.com/p/0123456789abcdef0123456789abcdef"}}}`))
	target := writerRevision("target", "session", "target_document", 1, "provider_sync", json.RawMessage(`{"data":{"adapter":"feishu","doc_id":"doc-1","uri":"https://tenant.feishu.cn/docx/doc-1"}}`))
	mustCreateWriterRecord(t, db.DB.Create(&draft).Error)
	mustCreateWriterRecord(t, db.DB.Create(&target).Error)

	slots := []slotDTO{toSlotDTO(&target), toSlotDTO(&draft)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	got := slots[1]
	if !got.WriteBackReady || got.WriteBackDirty || got.WriteBackState != writerWriteBackSyncedClean ||
		got.Provider != "notion" || got.ProviderDocumentID != "page-2" ||
		got.WriteBackURL != "https://app.notion.com/p/0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected Notion synced state: %+v", got)
	}
}

func TestWriterProviderURLRejectsUntrustedNotionLookalike(t *testing.T) {
	if got := writerProviderURL("notion", "https://app.notion.com.evil.example/p/page-2"); got != "" {
		t.Fatalf("expected lookalike domain to be rejected, got %q", got)
	}
}

func TestApplyWriterProviderBindingUsesWeChatBrowserURL(t *testing.T) {
	info := writerWriteBackInfo{}
	applyWriterProviderBinding(&info, writerProviderBinding{
		Provider:   "wechat",
		DocumentID: "draft-1",
		URI:        "",
		BrowserURL: "https://mp.weixin.qq.com/s?tempkey=fixture-preview",
	})
	if info.URL != "https://mp.weixin.qq.com/s?tempkey=fixture-preview" {
		t.Fatalf("write-back URL = %q, want WeChat draft URL", info.URL)
	}
}

func TestInternalProviderBindingsExposeSameDocumentInPublicationAndSlot(t *testing.T) {
	for _, tt := range []struct{ provider, uri, id, want string }{
		{"feishu", "feishu:/~docx/FixtureDoc", "FixtureDoc", "https://feishu.cn/docx/FixtureDoc"},
		{"feishu", "feishu@FixtureSpace:/~node/FixtureNode", "FixtureDoc", "https://feishu.cn/wiki/FixtureNode"},
		{"feishu", "", "FixtureDoc", "https://feishu.cn/docx/FixtureDoc"},
		{"notion", "notion:/~page/12345678-1234-1234-1234-123456789abc", "12345678-1234-1234-1234-123456789abc", "https://www.notion.so/12345678123412341234123456789abc"},
	} {
		t.Run(tt.provider+tt.uri, func(t *testing.T) {
			binding := writerProviderBinding{Provider: tt.provider, URI: tt.uri, DocumentID: tt.id}
			value, _ := json.Marshal(map[string]any{"provider_binding": binding})
			target := publicationBindingTarget(value)
			if got := documentPublicationTargetURL(tt.provider, target); got != tt.want {
				t.Fatalf("publication target=%s want=%s", got, tt.want)
			}
			info := writerWriteBackInfo{}
			applyWriterProviderBinding(&info, binding)
			if info.URL != tt.want {
				t.Fatalf("slot target=%s want=%s", info.URL, tt.want)
			}
		})
	}
}

func TestCanonicalWriterWriteBackProviderSupportsObsidian(t *testing.T) {
	if !writerProviderSupported("obsidian") {
		t.Fatal("Obsidian should be a supported write-back provider")
	}
	if got := canonicalWriterWriteBackProvider("obsidian"); got != "obsidian" {
		t.Fatalf("canonical provider = %q, want obsidian", got)
	}
	if got := writerProviderURL("obsidian", "obsidian://vlt_test/Note.md"); got != "" {
		t.Fatalf("Obsidian should not expose a browser URL, got %q", got)
	}
}

func TestEnrichWriterWriteBackSlots_UsesObsidianLocalPath(t *testing.T) {
	db := newTestDB(t)
	draft := writerRevision("draft", "session", "draft_document", 2, "provider_sync", json.RawMessage(
		`{"schema":"text/markdown","data":"# Note\n"}`,
	))
	target := writerRevision("target", "session", "target_document", 1, "provider_sync", json.RawMessage(
		`{"data":{"doc_id":"vlt_test:Note.md","uri":"obsidian://vlt_test/Note.md","adapter":"obsidian","meta":{"local_path":"/Users/test/Documents/obs/Note.md"}}}`,
	))
	mustCreateWriterRecord(t, db.DB.Create(&draft).Error)
	mustCreateWriterRecord(t, db.DB.Create(&target).Error)

	slots := []slotDTO{toSlotDTO(&target), toSlotDTO(&draft)}
	enrichSlots(context.Background(), db.DB, "session", slots)
	got := slots[1]
	if got.WriteBackState != writerWriteBackSyncedClean ||
		got.WriteBackURL != "obsidian://open?path=%2FUsers%2Ftest%2FDocuments%2Fobs%2FNote.md" ||
		got.WriteBackLocalPath != "/Users/test/Documents/obs/Note.md" {
		t.Fatalf("unexpected Obsidian write-back projection: %+v", got)
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
