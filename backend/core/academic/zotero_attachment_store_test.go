package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

func zoteroStorageFixture(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	db := paperLibraryTestDB(t, orm.OpenTestDB(t).DB)
	previousTransport := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/source-access/by-dataset:batch") {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"papers","exists":false,"allowed":true}]}`))}, nil
		}
		return previousTransport.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	if err := db.AutoMigrate(&orm.PaperLibrary{}, &orm.PaperLibraryLegacyState{}, &orm.UploadedFile{}, &orm.UserUIPreferences{}, &orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	graph := paperlibrary.EmptyData()
	paper := libraryDraft("paper", "Zotero paper", "")
	paper.Source = "zotero"
	paper.AttachmentIDs = []string{"zotero-attachment"}
	graph.Papers = []paperlibrary.Paper{*paper}
	graph.Attachments = []paperlibrary.Attachment{{ID: "zotero-attachment", PaperID: "paper", Name: "article.pdf", Kind: "pdf", Source: "zotero", IsPrimary: true, AddedAt: time.Now().UTC()}}
	raw, _ := json.Marshal(graph)
	if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", raw); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "article.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\nZotero attachment\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestZoteroAttachmentCommitFailureRollsBackDocumentAndRetryIsIdempotent(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	if err := db.Callback().Update().Before("gorm:update").Register("fail_zotero_graph", func(tx *gorm.DB) {
		if tx.Statement.Table == "paper_library_attachments" {
			tx.AddError(errors.New("disk unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
	if err == nil {
		t.Fatal("attachment association error was hidden")
	}
	for _, model := range []any{&orm.Document{}, &orm.Task{}} {
		var count int64
		db.Model(model).Count(&count)
		if count != 0 {
			t.Fatalf("failed association left %d %T", count, model)
		}
	}
	db.Callback().Update().Remove("fail_zotero_graph")
	for i := 0; i < 2; i++ {
		result, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
		if err != nil || !strings.Contains(string(marshal(result.Data)), `"contentRef":"zotero_`) {
			t.Fatalf("retry failed: %+v %v", result.Data, err)
		}
	}
	for _, model := range []any{&orm.Document{}, &orm.Task{}} {
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
	db.Model(&orm.Dataset{}).Where("id = ?", "papers").Update("processing_level", "parsed")
	db.Callback().Query().Before("gorm:query").Register("cancel_zotero_submission", func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			cancel()
		}
	})
	_, err := storeZoteroFile(ctx, db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
	if err == nil {
		t.Fatal("cancellation was concealed")
	}
	db.Callback().Query().Remove("cancel_zotero_submission")
	db.Model(&orm.Dataset{}).Where("id = ?", "papers").Update("processing_level", "stored")
	result, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
	if err != nil || !strings.Contains(string(marshal(result.Data)), "contentRef") {
		t.Fatalf("recovery failed: %+v %v", result.Data, err)
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate documents: %d", count)
	}
}

func TestZoteroAttachmentProcessingFailureIsLinkedAndReported(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	db.Model(&orm.Dataset{}).Where("id = ?", "papers").Update("processing_level", "parsed")
	now := time.Now().UTC()
	db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "owner", "document_parsing_enabled": false, "created_at": now, "updated_at": now})
	result, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
	if err == nil || !strings.Contains(string(marshal(result.Data)), "contentRef") || !strings.Contains(string(marshal(result.Data)), "processingError") {
		t.Fatalf("processing failure lost: %+v %v", result.Data, err)
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
	result, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
	if err == nil || !strings.Contains(string(marshal(result.Data)), "contentRef") || !strings.Contains(string(marshal(result.Data)), "processingError") {
		t.Fatalf("stored task failure was hidden: %+v %v", result.Data, err)
	}
}

func TestZoteroAttachmentRejectsMissingTargetAndInvalidPDF(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	_, err := storeZoteroFile(t.Context(), db, "papers", "missing", "zotero-attachment", path, "article.pdf", "owner", "Owner")
	if err == nil {
		t.Fatal("missing paper accepted")
	}
	os.WriteFile(path, []byte("<html>Login required</html>"), 0600)
	_, err = storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
	if err == nil {
		t.Fatal("invalid PDF accepted")
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 0 {
		t.Fatalf("invalid upload created %d documents", count)
	}
}

func TestZoteroRetryKeepsAcceptedTaskAndBlocksAmbiguousSubmission(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted_waiting", true: "unconfirmed_retry"}[ambiguous], func(t *testing.T) {
			db, path := zoteroStorageFixture(t)
			if _, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner"); err != nil {
				t.Fatal(err)
			}
			db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("processing_level", "parsed")
			now := time.Now().UTC()
			if err := db.Create(&orm.UserUIPreferences{UserID: "owner", DocumentParsingEnabled: false, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			ext := map[string]any{"task_state": "WAITING", "processing_level": "parsed"}
			if ambiguous {
				ext["market_submission"] = "retrying"
			}
			if err := db.Model(&orm.Task{}).Where("dataset_id=?", "papers").Update("ext", marshal(ext)).Error; err != nil {
				t.Fatal(err)
			}
			_, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner")
			if ambiguous && err == nil {
				t.Fatal("ambiguous submit was hidden")
			}
			if !ambiguous && err != nil {
				t.Fatalf("accepted task resubmitted: %v", err)
			}
			var task orm.Task
			db.Where("dataset_id=?", "papers").Take(&task)
			var saved map[string]any
			json.Unmarshal(task.Ext, &saved)
			if saved["task_state"] != "WAITING" {
				t.Fatalf("retry corrupted accepted/unknown state: %s", task.Ext)
			}
		})
	}
}
