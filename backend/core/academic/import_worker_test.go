package academic

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"io"
	"net/http"
	"net/http/httptest"
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
			for _, value := range []any{
				&orm.Dataset{ID: "target", ProcessingLevel: doc.ProcessingLevelStored, BaseModel: orm.BaseModel{CreateUserID: "alice"}},
				&orm.Document{ID: "doc", DatasetID: "target"}, &orm.Task{ID: "task", DatasetID: "target", DocID: "doc"},
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
	db, batch, item, _ := importPipelineFixture(t)
	batch.EntryType, batch.Status = "single_reference", "failed"
	item.Status = "failed"
	for _, row := range []any{&batch, &item} {
		if err := db.Save(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.AsyncJob{}).Where("id=?", batch.AsyncJobID).Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	retry := func() *httptest.ResponseRecorder {
		req := mux.SetURLVars(httptest.NewRequest("POST", "/academic/imports/batch/retry", nil), map[string]string{"batch_id": batch.ID})
		req.Header.Set("X-User-Id", "alice")
		rec := httptest.NewRecorder()
		RetryImport(rec, req)
		return rec
	}
	if rec := retry(); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var saved orm.PaperImportItem
	if err := db.Where("id=?", item.ID).Take(&saved).Error; err != nil || saved.Status != "pending" {
		t.Fatalf("not requeued: %+v %v", saved, err)
	}
	if again := retry(); again.Code != http.StatusConflict {
		t.Fatal("queued duplicate retry", again.Code)
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 2 {
		t.Fatal("unexpected queued tasks", count)
	}
}
