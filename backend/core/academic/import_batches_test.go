package academic

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestImportBatchesPageCompleteOperationsAndNotificationTotals(t *testing.T) {
	db := paperLibraryTestDB(t)
	now := time.Now().UTC()
	for n := 0; n < 22; n++ {
		batch := orm.PaperImportBatch{ID: fmt.Sprintf("batch-%02d", n), TargetDatasetID: "papers", CreatedBy: "owner", EntryType: "bibliography", Status: "succeeded", SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`), CreatedAt: now.Add(time.Duration(n) * time.Second), UpdatedAt: now}
		if err := db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
		count := 1
		if n == 21 {
			count = 205
		}
		for i := 0; i < count; i++ {
			status, kind := "succeeded", "metadata_only"
			if n == 0 {
				status, kind = "failed", "failed"
			}
			item := orm.PaperImportItem{ID: fmt.Sprintf("item-%02d-%03d", n, i), BatchID: batch.ID, TitleSnapshot: fmt.Sprintf("Paper %d", i), Status: status, ResultKind: kind, Stage: "done", ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	request := httptest.NewRequest("GET", "/academic/import-batches?target_dataset_id=papers", nil)
	request.Header.Set("X-User-Id", "owner")
	response := httptest.NewRecorder()
	ListImportBatches(response, request)
	if response.Code != 200 {
		t.Fatalf("list: %d %s", response.Code, response.Body.String())
	}
	var page struct {
		Items        []ImportOperation `json:"items"`
		Next         string            `json:"next_cursor"`
		Notification struct {
			NeedsAction int `json:"needs_action"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 20 || page.Items[0].Summary.Total != 205 || page.Items[0].Summary.MetadataOnly != 205 || page.Next == "" || page.Notification.NeedsAction != 1 {
		t.Fatalf("incomplete summary: %s", response.Body.String())
	}
	detail := httptest.NewRequest("GET", "/academic/import-batches/batch-21", nil)
	detail.Header.Set("X-User-Id", "owner")
	detail = mux.SetURLVars(detail, map[string]string{"batch_id": "batch-21"})
	response = httptest.NewRecorder()
	GetImportBatch(response, detail)
	var body struct {
		Operation ImportOperation       `json:"operation"`
		Items     []ImportOperationItem `json:"items"`
		Next      string                `json:"next_cursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || body.Operation.Summary.Total != 205 || len(body.Items) != 20 || body.Next == "" {
		t.Fatalf("detail truncated batch: %s", response.Body.String())
	}
	request = httptest.NewRequest("GET", "/academic/import-batches?target_dataset_id=papers&cursor="+page.Next, nil)
	request.Header.Set("X-User-Id", "owner")
	response = httptest.NewRecorder()
	ListImportBatches(response, request)
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Next != "" {
		t.Fatalf("bad next page: %s", response.Body.String())
	}
	detail.Header.Set("X-User-Id", "other-user")
	response = httptest.NewRecorder()
	GetImportBatch(response, detail)
	if response.Code != 404 {
		t.Fatalf("foreign batch accessible: %d", response.Code)
	}
}

func TestImportOperationPreservesSkippedAndLaterResolvedFailure(t *testing.T) {
	db := paperLibraryTestDB(t)
	db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("processing_level", "stored")
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF-test"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	work := orm.AcademicWork{ID: "work", CanonicalTitle: "Original title", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	document := orm.Document{ID: "pdf", DatasetID: "papers", Ext: marshal(map[string]string{"stored_path": path})}
	link := orm.AcademicWorkDocument{AcademicWorkID: work.ID, DatasetID: "papers", DocumentID: document.ID, SourceProvider: "arxiv", SourceVersion: "v2"}
	for _, row := range []any{&work, &document, &link} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	batches := []orm.PaperImportBatch{}
	for _, status := range []string{"failed", "succeeded", "skipped"} {
		batch := orm.PaperImportBatch{ID: status, TargetDatasetID: "papers", CreatedBy: "owner", Status: status, EntryType: "single_reference", SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
		batches = append(batches, batch)
		item := orm.PaperImportItem{ID: status, BatchID: batch.ID, AcademicWorkID: work.ID, TitleSnapshot: "Snapshot title", Status: status, ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{"source_provider":"arxiv","version":"v2"}`), ErrorDetailsJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
		if status != "failed" {
			item.DocumentID = document.ID
		}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	operations, err := loadImportOperations(t.Context(), db, batches, true, map[string]*ImportQueueDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if !operations[0].Items[0].ResolvedLater || operations[0].Items[0].Status != "failed" || operations[0].Summary.NeedsAction != 0 || operations[0].Summary.Retryable != 0 {
		t.Fatalf("historical failure overwritten: %+v", operations[0])
	}
	if operations[2].Items[0].CurrentState != "skipped" || operations[2].Summary.Skipped != 1 || operations[2].Items[0].Title != "Snapshot title" {
		t.Fatalf("skip misreported: %+v", operations[2])
	}
	if err := db.Delete(&document).Error; err != nil {
		t.Fatal(err)
	}
	operations, err = loadImportOperations(t.Context(), db, batches, false, map[string]*ImportQueueDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if !operations[1].Items[0].DocumentDeleted || operations[1].Summary.NeedsAction != 0 {
		t.Fatalf("deletion created a false failure: %+v", operations[1])
	}
}

func TestLibraryImportReceiptIsAtomicAndIdempotent(t *testing.T) {
	db := paperLibraryTestDB(t)
	save := func(key, title string, revision int64) *httptest.ResponseRecorder {
		return paperLibraryRequest(t, "PUT", "papers", "owner", fmt.Sprintf(`{"revision":%d,"data":%s,"import_context":{"idempotency_key":%q,"entry_type":"bibliography","format":"bib","items":[{"paper_id":"paper"}]}}`, revision, paperLibraryFixture(title), key))
	}
	first := save("first", "New paper", 0)
	if first.Code != 200 {
		t.Fatalf("save: %d %s", first.Code, first.Body.String())
	}
	conflict := save("conflict", "Must not save", 0)
	if conflict.Code != 409 {
		t.Fatalf("conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	replay := save("first", "Stale draft", 0)
	if replay.Code != 200 || strings.Contains(replay.Body.String(), "Stale draft") {
		t.Fatalf("replay overwrote graph: %s", replay.Body.String())
	}
	var count int64
	db.Model(&orm.PaperImportBatch{}).Count(&count)
	if count != 1 {
		t.Fatalf("created %d operations", count)
	}
	second := save("second", "New paper", 1)
	if second.Code != 200 {
		t.Fatalf("second import: %d %s", second.Code, second.Body.String())
	}
	var batch orm.PaperImportBatch
	db.Where("idempotency_key=?", "second").Take(&batch)
	if batch.SkippedItems != 1 || batch.TotalItems != 1 {
		t.Fatalf("existing metadata misreported: %+v", batch)
	}
}
