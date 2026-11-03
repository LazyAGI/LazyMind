package academic

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

// Version identity, concurrent deduplication and atomic association recovery now
// run through TestUnifiedPaperImportReusesCacheAndKeepsDifferentVersions,
// TestUnifiedConcurrentImportsPublishOneVersionedDocument and
// TestUnifiedImportAssociationFailureRollsBackDocumentAndReceipt. Keep the
// library's independent parser-status and attachment-availability regressions.
func TestUnifiedArxivLibraryDistinguishesParserAcknowledgementsFromErrors(t *testing.T) {
	for _, tt := range []struct{ name, state, code, message, want string }{
		{"real success callback", "SUCCESS", "200", "success", "ready"},
		{"working acknowledgement", "WORKING", "200", "success", "parsing"},
		{"real failure", "FAILED", "KeyError", "No source is configured for dynamic embedding source.", "failed"},
		{"success carrying actual error", "SUCCESS", "KeyError", "No source is configured for dynamic embedding source.", "failed"},
		{"success carrying error code only", "SUCCESS", "KeyError", "", "failed"},
		{"success carrying nonempty exception", "SUCCESS", "200", "upstream parse exception", "failed"},
		{"failed state with acknowledgement text", "FAILED", "200", "success", "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
			db := paperLibraryTestDB(t)
			if err := db.AutoMigrate(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Updates(map[string]any{"kb_id": "remote-papers", "processing_level": "chunked", "processing_revision": 1}).Error; err != nil {
				t.Fatal(err)
			}
			paper := importDraft(t, "papers", "arxiv", libraryDraft("paper", "Paper", "10.1234/arxiv")).Data.Papers[0]
			path := filepath.Join(t.TempDir(), "paper.pdf")
			if err := os.WriteFile(path, []byte("%PDF fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, row := range []any{
				&orm.Document{ID: "pdf", DatasetID: "papers", LazyllmDocID: "remote-pdf", DisplayName: "paper.pdf", Ext: marshal(map[string]string{"stored_path": path})},
				&orm.PaperLibraryAttachment{DatasetID: "papers", PaperID: paper.ID, ID: "attachment", DocumentID: "pdf", Name: "paper.pdf", Kind: "pdf", IsPrimary: true},
				&orm.DocumentProcessingState{DatasetID: "papers", DocumentID: "pdf", ParseStatus: "pending", ChunkStatus: "pending", IndexStatus: "pending", UpdatedAt: now},
				&orm.Task{ID: "task", DatasetID: "papers", DocID: "pdf", LazyllmTaskID: "remote-task", Ext: marshal(map[string]any{"task_state": "RUNNING", "processing_level": "chunked", "processing_revision": 1}), BaseModel: orm.BaseModel{CreatedAt: now}},
				&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "remote-task", DocID: "remote-pdf", KbID: "remote-papers", Status: tt.state, ErrorCode: &tt.code, ErrorMsg: &tt.message, CreatedAt: now, UpdatedAt: now},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			view := readLibrary(t, "papers")
			if len(view.Data.Papers) != 1 || view.Data.Papers[0].Parse != tt.want {
				t.Fatalf("parser result misclassified: %+v", view.Data.Papers)
			}
			message := view.Data.Papers[0].ProcessingError
			if tt.want == "failed" && (message == "" || message == "success") {
				t.Fatalf("failure lacks meaningful message: %q", message)
			}
			if tt.want != "failed" && message != "" {
				t.Fatalf("acknowledgement presented as error: %q", message)
			}
		})
	}
}

func TestUnifiedArxivLibraryMarksMissingAndDeletedAttachmentsUnavailable(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted_document", true: "missing_file"}[missing], func(t *testing.T) {
			db := paperLibraryTestDB(t)
			paper := importDraft(t, "papers", "arxiv", libraryDraft("paper", "Paper", "10.1234/arxiv")).Data.Papers[0]
			path := filepath.Join(t.TempDir(), "paper.pdf")
			if err := os.WriteFile(path, []byte("%PDF fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, row := range []any{
				&orm.Document{ID: "pdf", DatasetID: "papers", DisplayName: "paper.pdf", Ext: marshal(map[string]string{"stored_path": path})},
				&orm.PaperLibraryAttachment{DatasetID: "papers", PaperID: paper.ID, ID: "attachment", DocumentID: "pdf", Name: "paper.pdf", Kind: "pdf", IsPrimary: true},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if view := readLibrary(t, "papers"); len(view.Data.Attachments) != 1 || !view.Data.Attachments[0].Available {
				t.Fatal("control attachment unavailable", view)
			}
			if missing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := db.Model(&orm.Document{}).Where("id=?", "pdf").Update("deleted_at", time.Now().UTC()).Error; err != nil {
				t.Fatal(err)
			}
			view := readLibrary(t, "papers")
			if len(view.Data.Papers) != 1 || len(view.Data.Attachments) != 1 || view.Data.Attachments[0].Available {
				t.Fatalf("unavailable PDF exposed or bibliography lost: papers=%d attachments=%+v", len(view.Data.Papers), view.Data.Attachments)
			}
			// Persisted membership remains recoverable, rather than deleting the paper.
			var attachment orm.PaperLibraryAttachment
			if err := db.Where("id=?", "attachment").Take(&attachment).Error; err != nil {
				t.Fatal(err)
			}
		})
	}
}
