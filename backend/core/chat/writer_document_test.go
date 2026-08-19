package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestWriterSyncStatus(t *testing.T) {
	for input, want := range map[int]int{
		http.StatusBadRequest:          http.StatusBadRequest,
		http.StatusUnprocessableEntity: http.StatusBadRequest,
		http.StatusUnauthorized:        http.StatusUnauthorized,
		http.StatusForbidden:           http.StatusForbidden,
		http.StatusConflict:            http.StatusConflict,
		http.StatusInternalServerError: http.StatusBadGateway,
	} {
		if got := writerSyncStatus(input); got != want {
			t.Errorf("writerSyncStatus(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestNormalizeWriterDocumentForSync_StripsLegacyImagePlaceholderNewline(t *testing.T) {
	normalized, err := normalizeWriterDocumentForSync(json.RawMessage(`{
		"blocks":[
			{"node_id":"image-1","type":"image","content":"\n\ncaption","spans":[{"text":"\n\ncaption","style":[]}]},
			{"node_id":"paragraph-1","type":"paragraph","content":"\nkeep this newline","spans":[{"text":"\nkeep this newline","style":[]}]}
		]
	}`))
	if err != nil {
		t.Fatalf("normalize WriterDocument: %v", err)
	}
	var document struct {
		Blocks []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Spans   []struct {
				Text string `json:"text"`
			} `json:"spans"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(normalized, &document); err != nil {
		t.Fatalf("decode normalized WriterDocument: %v", err)
	}
	if document.Blocks[0].Content != "caption" || document.Blocks[0].Spans[0].Text != "caption" || document.Blocks[1].Content != "\nkeep this newline" {
		t.Fatalf("unexpected normalized blocks: %+v", document.Blocks)
	}
}

func TestPreserveExistingWriterImageBlocks(t *testing.T) {
	source := json.RawMessage(`{
		"blocks":[
			{"node_id":"paragraph-1","type":"paragraph","content":"before"},
			{"node_id":"image-1","type":"image","content":"saved caption","metadata":{"asset":"asset-1"}}
		]
	}`)
	revised := json.RawMessage(`{
		"blocks":[
			{"node_id":"paragraph-1","type":"paragraph","content":"edited text"},
			{"node_id":"image-1","type":"image","content":"\n\nsaved caption","spans":[{"text":"\n\nsaved caption","style":[]}]},
			{"node_id":"image-new","type":"image","content":"new image"}
		]
	}`)

	preserved, err := preserveExistingWriterImageBlocks(source, revised)
	if err != nil {
		t.Fatalf("preserve Writer image blocks: %v", err)
	}
	var document struct {
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(preserved, &document); err != nil {
		t.Fatalf("decode preserved WriterDocument: %v", err)
	}
	image := document.Blocks[1]
	_, hasSpans := image["spans"]
	if document.Blocks[0]["content"] != "edited text" || image["content"] != "saved caption" || hasSpans || document.Blocks[2]["content"] != "new image" {
		t.Fatalf("unexpected preserved blocks: %+v", document.Blocks)
	}
}

func TestWriterDocumentIsUnbound(t *testing.T) {
	if !writerDocumentIsUnbound(json.RawMessage(`{"document_id":"local","blocks":[],"provider_binding":{}}`)) {
		t.Fatal("local WriterDocument should be unbound")
	}
	if writerDocumentIsUnbound(json.RawMessage(`{"document_id":"cloud","blocks":[],"provider_binding":{"provider":"feishu","document_id":"doc-1"}}`)) {
		t.Fatal("Feishu WriterDocument should not be unbound")
	}
}

func TestWriterDocumentRenderSlotIncludesSource(t *testing.T) {
	if slot, ok := writerDocumentRenderSlot("source_document"); !ok || slot != "source_document" {
		t.Fatalf("source_document render slot = %q, %v", slot, ok)
	}
	if _, ok := writerDocumentSlot("source_document"); ok {
		t.Fatal("source_document must remain read-only")
	}
}

func TestLoadWriterWriteBackArtifact_InlineMarkdown(t *testing.T) {
	artifact, err := loadWriterWriteBackArtifact(json.RawMessage(
		`{"schema":"text/markdown","data":"# Draft\n"}`,
	))
	if err != nil {
		t.Fatalf("load inline Markdown: %v", err)
	}
	if artifact.Format != "markdown" || artifact.Markdown != "# Draft\n" || artifact.Title != "Draft" {
		t.Fatalf("unexpected inline Markdown artifact: %+v", artifact)
	}
}

func TestLoadWriterWriteBackBaseline_UsesSourceDocumentForInitialSync(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSlotRevision{})
	source := json.RawMessage(`{"data":{"document_id":"feishu-doc","provider_binding":{"provider":"feishu","document_id":"feishu-doc"}}}`)
	seedWriterRevision(t, db, "source", "source_document", 1, true, "ai", source)
	seedWriterRevision(t, db, "draft-1", "draft_document", 1, false, "ai", source)
	seedWriterRevision(t, db, "draft-2", "draft_document", 2, true, "human", source)

	baseline, err := loadWriterWriteBackBaseline(context.Background(), db.DB, "session", 2)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if baseline.Revision.SlotID != "source_document" {
		t.Fatalf("baseline slot = %q, want source_document", baseline.Revision.SlotID)
	}
	if baseline.Revision.Revision != 1 {
		t.Fatalf("baseline revision = %d, want 1", baseline.Revision.Revision)
	}
}

func TestLoadWriterWriteBackBaseline_PrefersLatestSyncedDraft(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSlotRevision{})
	source := json.RawMessage(`{"data":{"document_id":"source-doc","provider_binding":{"provider":"feishu","document_id":"source-doc"}}}`)
	syncedDraft := json.RawMessage(`{"data":{"document_id":"synced-doc","provider_binding":{"provider":"feishu","document_id":"synced-doc"}},"meta":{"lazymind_provider_sync":{"confirmed":true}}}`)
	seedWriterRevision(t, db, "source", "source_document", 1, true, "ai", source)
	seedWriterRevision(t, db, "draft-1", "draft_document", 1, false, "host", syncedDraft)
	seedWriterRevision(t, db, "draft-2", "draft_document", 2, false, "human", syncedDraft)
	seedWriterRevision(t, db, "draft-3", "draft_document", 3, true, "human", syncedDraft)

	baseline, err := loadWriterWriteBackBaseline(context.Background(), db.DB, "session", 3)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if baseline.Revision.ID != "draft-1" {
		t.Fatalf("baseline id = %q, want draft-1", baseline.Revision.ID)
	}
}

func seedWriterRevision(
	t *testing.T,
	db *orm.DB,
	id, slotID string,
	revision int,
	selected bool,
	changeSource string,
	content json.RawMessage,
) {
	t.Helper()
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID:              id,
		SessionID:       "session",
		SlotID:          slotID,
		Revision:        revision,
		Selected:        selected,
		ContentSnapshot: content,
		ChangeSource:    changeSource,
		Slot:            slotID,
		StepID:          "write_document",
		Attempt:         1,
		CreatedAt:       time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed revision %s: %v", id, err)
	}
}
