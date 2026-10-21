package academic

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func zoteroStorageFixture(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	svc, _ := arxivImportTestService(t)
	db := svc.db
	if err := db.AutoMigrate(&orm.PaperLibrary{}, &orm.UploadedFile{}, &orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	var graph map[string]any
	json.Unmarshal([]byte(paperLibraryFixture("Zotero paper")), &graph)
	graph["attachments"] = []any{map[string]any{"id": "zotero-attachment", "paperId": "paper", "name": "article.pdf", "kind": "pdf", "source": "zotero", "addedAt": "2026-10-22"}}
	graph["papers"].([]any)[0].(map[string]any)["attachmentIds"] = []any{"zotero-attachment"}
	raw, _ := json.Marshal(graph)
	if _, err := savePaperLibrary(t.Context(), db, "dataset", 0, raw); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "article.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\nZotero attachment"), 0600); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestZoteroAttachmentRetryReusesDocumentAfterGraphWriteFailure(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	if err := db.Callback().Update().Before("gorm:update").Register("fail_zotero_graph", func(tx *gorm.DB) {
		if tx.Statement.Table == "paper_libraries" {
			tx.AddError(errors.New("disk unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := storeZoteroFile(t.Context(), db, "dataset", "paper", "zotero-attachment", path, "article.pdf", "user", "User")
	if err == nil {
		t.Fatal("graph save error was hidden")
	}
	db.Callback().Update().Remove("fail_zotero_graph")
	for i := 0; i < 2; i++ {
		result, err := storeZoteroFile(t.Context(), db, "dataset", "paper", "zotero-attachment", path, "article.pdf", "user", "User")
		if err != nil || !strings.Contains(string(result.Data), `"contentRef":"zotero_`) {
			t.Fatalf("retry failed: %s %v", result.Data, err)
		}
	}
	for _, model := range []any{&orm.Document{}, &orm.Task{}, &orm.UploadedFile{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("duplicated %T: %d %v", model, count, err)
		}
	}
}

func TestZoteroAttachmentResumesCancelledSubmission(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "parsed")
	db.Callback().Query().Before("gorm:query").Register("cancel_zotero_submission", func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			cancel()
		}
	})
	_, err := storeZoteroFile(ctx, db, "dataset", "paper", "zotero-attachment", path, "article.pdf", "user", "User")
	if err == nil {
		t.Fatal("cancellation was concealed")
	}
	db.Callback().Query().Remove("cancel_zotero_submission")
	db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "stored")
	result, err := storeZoteroFile(t.Context(), db, "dataset", "paper", "zotero-attachment", path, "article.pdf", "user", "User")
	if err != nil || !strings.Contains(string(result.Data), "contentRef") {
		t.Fatalf("recovery failed: %s %v", result.Data, err)
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate documents: %d", count)
	}
}

func TestZoteroAttachmentProcessingFailureIsLinkedAndReported(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "parsed")
	now := time.Now().UTC()
	db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "user", "document_parsing_enabled": false, "created_at": now, "updated_at": now})
	result, err := storeZoteroFile(t.Context(), db, "dataset", "paper", "zotero-attachment", path, "article.pdf", "user", "User")
	if err == nil || !strings.Contains(string(result.Data), "contentRef") || !strings.Contains(string(result.Data), "processingError") {
		t.Fatalf("processing failure lost: %s %v", result.Data, err)
	}
}

func TestZoteroAttachmentStoredTaskFailureIsLinkedAndReported(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	if err := db.Callback().Update().Before("gorm:update").Register("fail_zotero_stored_task", func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			tx.AddError(errors.New("task storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Update().Remove("fail_zotero_stored_task") })
	result, err := storeZoteroFile(t.Context(), db, "dataset", "paper", "zotero-attachment", path, "article.pdf", "user", "User")
	if err == nil || !strings.Contains(string(result.Data), "contentRef") || !strings.Contains(string(result.Data), "processingError") {
		t.Fatalf("stored task failure was hidden: %s %v", result.Data, err)
	}
}

func TestZoteroAttachmentRejectsMissingTargetAndInvalidPDF(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	_, err := storeZoteroFile(t.Context(), db, "dataset", "missing", "zotero-attachment", path, "article.pdf", "user", "User")
	if err == nil {
		t.Fatal("missing paper accepted")
	}
	os.WriteFile(path, []byte("<html>Login required</html>"), 0600)
	_, err = storeZoteroFile(t.Context(), db, "dataset", "paper", "zotero-attachment", path, "article.pdf", "user", "User")
	if err == nil {
		t.Fatal("invalid PDF accepted")
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 0 {
		t.Fatalf("invalid upload created %d documents", count)
	}
}
