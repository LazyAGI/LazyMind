package academic

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestBothImportEntrypointsResumeTheCommittedOriginal(t *testing.T) {
	for _, entry := range []string{"conversation_sources", "single_reference"} {
		t.Run(entry, func(t *testing.T) {
			db := sourceTestDB(t)
			if err := db.AutoMigrate(&orm.Dataset{}, &orm.Task{}, &orm.ACLModel{}); err != nil {
				t.Fatal(err)
			}
			previous := store.DB()
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(previous, previous, nil) })
			transport := http.DefaultTransport
			http.DefaultTransport = sourceTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"target","exists":false,"allowed":true}],"source_map":{}}`))}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = transport })
			t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
			batch := orm.PaperImportBatch{ID: "batch", EntryType: entry, AsyncJobID: "job", CreatedBy: "alice", TargetDatasetID: "target", Status: "running", PolicySnapshotJSON: json.RawMessage(`{}`), SourceDocumentIDsJSON: json.RawMessage(`[]`)}
			item := orm.PaperImportItem{ID: "item", BatchID: batch.ID, DocumentID: "doc", DocumentTaskID: "task", Stage: "processing", Status: "running", ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{"url":"must-not-be-downloaded"}`)}
			path := filepath.Join(t.TempDir(), "original.pdf")
			if err := os.WriteFile(path, []byte("%PDF fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, value := range []any{
				&orm.Dataset{ID: "target", ProcessingLevel: doc.ProcessingLevelStored, BaseModel: orm.BaseModel{CreateUserID: "alice"}},
				&orm.Document{ID: "doc", DatasetID: "target", Ext: marshal(map[string]string{"stored_path": path})}, &orm.Task{ID: "task", DatasetID: "target", DocID: "doc", Ext: json.RawMessage(`{}`)},
				&orm.AsyncJob{ID: "job", AttemptCount: 1, Status: "running", PayloadJSON: json.RawMessage(`{}`)}, &batch, &item,
			} {
				if err := db.Create(value).Error; err != nil {
					t.Fatal(err)
				}
			}
			submitted := 0
			originals, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, SubmitProcessing: func(_ context.Context, _ doc.DatasetCatalogCaller, dataset, task string) (doc.StartTaskResult, error) {
				if dataset != "target" || task != "task" {
					t.Fatalf("wrong binding: %s %s", dataset, task)
				}
				submitted++
				return doc.StartTaskResult{Status: "STARTED", Message: "document stored without parsing"}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, batch, item, sourceImportCaller(batch), originals); err != nil {
				t.Fatal(err)
			}
			var saved orm.PaperImportItem
			if err := db.Where("id = ?", item.ID).Take(&saved).Error; err != nil {
				t.Fatal(err)
			}
			var count int64
			db.Model(&orm.Document{}).Count(&count)
			if count != 1 || submitted != 1 || saved.DocumentID != "doc" || saved.Status != "succeeded" {
				t.Fatalf("resume duplicated or lost the original: documents=%d submitted=%d item=%+v", count, submitted, saved)
			}
		})
	}
}

func TestReferenceImportRetryResetsItemsAndPublishesJobAtomically(t *testing.T) {
	db := sourceTestDB(t)
	previous := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(previous, previous, nil) })
	batch := orm.PaperImportBatch{ID: "batch", EntryType: "single_reference", CreatedBy: "alice", AsyncJobID: "old-job", Status: "failed", PolicySnapshotJSON: json.RawMessage(`{}`), SourceDocumentIDsJSON: json.RawMessage(`[]`)}
	item := orm.PaperImportItem{ID: "item", BatchID: batch.ID, DocumentID: "committed-doc", DocumentTaskID: "committed-task", Status: "failed", Stage: "processing", ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{}`)}
	for _, row := range []any{&batch, &item, &orm.AsyncJob{ID: "old-job", Status: "failed", PayloadJSON: json.RawMessage(`{}`)}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	retry := func() *httptest.ResponseRecorder {
		req := mux.SetURLVars(httptest.NewRequest("POST", "/academic/imports/batch/retry", nil), map[string]string{"batch_id": batch.ID})
		req.Header.Set("X-User-Id", "alice")
		rec := httptest.NewRecorder()
		RetryImport(rec, req)
		return rec
	}
	rec := retry()
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var saved orm.PaperImportItem
	if err := db.Where("id = ?", item.ID).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "pending" || saved.DocumentID != item.DocumentID || saved.DocumentTaskID != item.DocumentTaskID {
		t.Fatalf("lost resumable binding: %+v", saved)
	}
	if err := db.Where("id = ?", batch.ID).Take(&batch).Error; err != nil {
		t.Fatal(err)
	}
	var job orm.AsyncJob
	if err := db.Where("id = ?", batch.AsyncJobID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "pending" || job.ResourceID != batch.ID || batch.Status != "pending" {
		t.Fatalf("job and batch disagree: %+v %+v", job, batch)
	}
	if again := retry(); again.Code != http.StatusConflict {
		t.Fatal("queued duplicate retry", again.Code)
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 2 {
		t.Fatal("unexpected queued jobs", count)
	}
}
