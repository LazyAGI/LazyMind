package academic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

func testVerifiedPaper() PaperRef {
	abs := "Verified external abstract"
	return PaperRef{PaperID: "a", Title: "Verified paper", Authors: []string{"Author"}, Abstract: &abs, ArxivID: "2404.06654", SourceProvider: "arxiv", SourceVersion: "v2", PDFURL: "https://arxiv.org/pdf/2404.06654v2"}
}
func testPDFCandidate() FulltextCandidate {
	return FulltextCandidate{URL: "https://arxiv.org/pdf/2404.06654v2", SourceProvider: "arxiv", Version: "v2", SourceType: "official_pdf", ExpectedMIME: "application/pdf"}
}

func TestImportReceiptValidatesOwnershipVersionAndSurvivesEnqueue(t *testing.T) {
	db := paperFixture(t)
	ref, candidate := testVerifiedPaper(), testPDFCandidate()
	item := PreviewItem{WorkID: "a", Paper: ref, Candidates: []FulltextCandidate{candidate}}
	token, err := saveImportPreview(t.Context(), "u", "papers", []PreviewItem{item}, map[string]PaperRef{"a": ref}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range [][2]string{{"other", "papers"}, {"u", "other"}} {
		if _, err = loadImportPreview(t.Context(), token, other[0], other[1]); err == nil {
			t.Fatal("receipt crossed its owner or collection")
		}
	}
	request := createImportRequest{PreviewToken: token, TargetDatasetID: "papers", Items: []selectedImportItem{{WorkID: "a", Candidate: candidate}}, IdempotencyKey: "receipt-test"}
	request.Items[0].Candidate.Version = "v1"
	w := httptest.NewRecorder()
	CreateImport(w, paperRequest(string(marshal(request))))
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	request.Items[0].Candidate = candidate
	w = httptest.NewRecorder()
	CreateImport(w, paperRequest(string(marshal(request))))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved orm.PaperImportItem
	if err = db.Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot savedImportCandidate
	if err = json.Unmarshal(saved.SelectedCandidateJSON, &snapshot); err != nil || snapshot.VerifiedPaper == nil || snapshot.VerifiedPaper.Title != ref.Title || snapshot.Version != "v2" {
		t.Fatalf("missing durable verified snapshot: %+v %v", snapshot, err)
	}
	if err = db.Model(&orm.PaperImportItem{}).Where("id=?", saved.ID).Update("status", "skipped").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&orm.PaperImportBatch{}).Where("id=?", saved.BatchID).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = "concurrent-receipt-test"
	encoded := string(marshal(request))
	start := make(chan struct{})
	responses := make([]*httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for i := range responses {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			response := httptest.NewRecorder()
			CreateImport(response, paperRequest(encoded))
			responses[index] = response
		}(i)
	}
	close(start)
	workers.Wait()
	var previousID string
	for _, response := range responses {
		var result struct {
			BatchID string `json:"batch_id"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.BatchID == "" {
			t.Fatal(response.Code, response.Body.String())
		}
		if previousID != "" && previousID != result.BatchID {
			t.Fatal("concurrent submit created another batch")
		}
		previousID = result.BatchID
	}
	receipt, _ := loadImportPreview(t.Context(), token, "u", "papers")
	receipt.ExpiresAt = time.Now().Add(-time.Minute)
	if err = store.State().Set(t.Context(), "academic:import-preview:"+token, marshal(receipt), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = loadImportPreview(t.Context(), token, "u", "papers"); err == nil {
		t.Fatal("accepted an expired receipt")
	}
}

func TestLocalPreviewDoesNotQueryArxiv(t *testing.T) {
	db := paperFixture(t)
	if err := db.Model(&orm.AcademicWork{}).Where("id=?", "a").Update("arxiv_id_base", "2404.06654").Error; err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == testArxivHost {
			calls++
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: http.Header{}, Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	installTestArxivClient(t)
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"target_dataset_id":"papers","work_ids":["a"],"resolve_external":false}`))
	if w.Code != 200 || calls != 0 {
		t.Fatal(w.Code, calls, w.Body.String())
	}
	var response struct {
		Items []PreviewItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Items) != 1 || response.Items[0].Disposition != "metadata_only" {
		t.Fatal("unresolved version advertised as downloadable", w.Body.String(), err)
	}
}

func TestPDFCacheIsolationCorruptionAndNightlyCleanup(t *testing.T) {
	svc, downloads := cacheTestFixture(t)
	ref := testVerifiedPaper()
	acquire := func(user string) (string, error) {
		guard, err := lockArxivImport(t.Context(), svc.db, ref.ArxivID)
		if err != nil {
			return "", err
		}
		defer guard.release()
		path, _, err := acquireCachedPDFLocked(guard.ctx, user, ref, svc.acquire)
		return path, err
	}
	path, err := acquire("user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = acquire("user"); err != nil || downloads.Load() != 1 {
		t.Fatal("cache missed", err, downloads.Load())
	}
	if _, err = acquire("other-user"); err != nil || downloads.Load() != 2 {
		t.Fatal("cache crossed users", err, downloads.Load())
	}
	if err = os.WriteFile(path, []byte("%PDF-corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = acquire("user"); err != nil || downloads.Load() != 3 {
		t.Fatal("corrupt file reused", err, downloads.Load())
	}
	directory := filepath.Dir(path)
	record, err := readCachedPDF(directory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 3, 0, 0, 0, paperPDFCleanupBoundary(time.Now()).Location())
	record.DownloadedAt = now.Add(-time.Minute)
	if err = os.WriteFile(filepath.Join(directory, "source.json"), marshal(record), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "unique.pdf")
	if err = os.WriteFile(outside, []byte("unique"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Dir(outside), filepath.Join(paperPDFCacheRoot(), "external-link")); err != nil {
		t.Fatal(err)
	}
	guard, err := lockArxivImport(t.Context(), svc.db, ref.ArxivID)
	if err != nil {
		t.Fatal(err)
	}
	count, _, err := cleanupPaperPDFs(t.Context(), svc.db, now)
	guard.release()
	if err != nil || count != 0 {
		t.Fatal("cleanup removed an active file", count, err)
	}
	count, _, err = cleanupPaperPDFs(t.Context(), svc.db, now)
	if err != nil || count != 1 {
		t.Fatal("nightly cleanup failed", count, err)
	}
	if _, err = os.Stat(outside); err != nil {
		t.Fatal("cleanup followed an external symlink", err)
	}
	if _, err = acquire("user"); err != nil || downloads.Load() != 4 {
		t.Fatal("missing file was not reacquired", err, downloads.Load())
	}
	partial := directory + ".partial-recovery"
	if err = os.Mkdir(partial, 0700); err != nil {
		t.Fatal(err)
	}
	record.DownloadedAt = now.Add(time.Minute)
	if err = os.WriteFile(filepath.Join(partial, "source.json"), marshal(record), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(partial, "unfinished.pdf"), []byte("%PDF-partial"), 0600); err != nil {
		t.Fatal(err)
	}
	count, _, err = cleanupPaperPDFs(t.Context(), svc.db, now)
	if err != nil || count != 1 {
		t.Fatal("startup left an inactive incomplete download", count, err)
	}
	if !paperPDFCleanupBoundary(now.Add(-time.Minute)).Equal(now.Add(-24 * time.Hour)) {
		t.Fatal("startup cleanup crossed the current night boundary")
	}
}

func TestImportRetryRequiresCurrentCollectionWritePermission(t *testing.T) {
	db := paperFixture(t)
	batch := orm.PaperImportBatch{ID: "revoked-batch", CreatedBy: "u", TargetDatasetID: "papers", Status: "failed", SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]string{})}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("create_user_id", "another-owner").Error; err != nil {
		t.Fatal(err)
	}
	request := mux.SetURLVars(paperRequest(`{}`), map[string]string{"batch_id": batch.ID})
	response := httptest.NewRecorder()
	RetryImport(response, request)
	if response.Code != 403 {
		t.Fatal(response.Code, response.Body.String())
	}
	var saved orm.PaperImportBatch
	if err := db.Take(&saved, "id=?", batch.ID).Error; err != nil || saved.Status != "failed" {
		t.Fatal("denied retry changed batch", saved.Status, err)
	}
}

func TestImportQueueFiltersSourceDocumentOnBothDatabases(t *testing.T) {
	db := paperFixture(t)
	for _, id := range []string{"a", "b"} {
		batch := orm.PaperImportBatch{ID: "source-" + id, CreatedBy: "u", TargetDatasetID: "papers", Status: "failed", SourceDocumentIDsJSON: marshal([]string{id}), PolicySnapshotJSON: marshal(map[string]string{})}
		item := orm.PaperImportItem{ID: "source-item-" + id, BatchID: batch.ID, AcademicWorkID: id, Status: "failed", SelectedCandidateJSON: marshal(FulltextCandidate{}), ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]string{}), ErrorDetailsJSON: marshal(map[string]string{})}
		if err := db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/academic/imports?target_dataset_id=papers&source_document_id=a", nil)
	request.Header.Set("X-User-Id", "u")
	response := httptest.NewRecorder()
	ListImports(response, request)
	var result struct {
		Items []ImportQueueItem `json:"items"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Items) != 1 || result.Items[0].ItemID != "source-item-a" {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestExpansionImportKeepsDocumentProvenance(t *testing.T) {
	db := paperFixture(t)
	record := orm.AcademicExpansion{ID: "source-expansion", UserID: "u", DatasetID: "papers", Version: 3, ResultJSON: marshal(ReviewWorkflowInput{})}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	ref, candidate := testVerifiedPaper(), testPDFCandidate()
	token, err := saveImportPreview(t.Context(), "u", "papers", []PreviewItem{{WorkID: "a", Paper: ref, Candidates: []FulltextCandidate{candidate}}}, map[string]PaperRef{"a": ref}, &record)
	if err != nil {
		t.Fatal(err)
	}
	request := createImportRequest{EntryType: "single_reference", PreviewToken: token, TargetDatasetID: "papers", Items: []selectedImportItem{{WorkID: "a", Candidate: candidate}}, IdempotencyKey: "expansion-origin"}
	response := httptest.NewRecorder()
	CreateImport(response, paperRequest(string(marshal(request))))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var batch orm.PaperImportBatch
	if err := db.Take(&batch).Error; err != nil {
		t.Fatal(err)
	}
	var origin struct {
		ExpansionID      string `json:"expansion_id"`
		ExpansionVersion int64  `json:"expansion_version"`
	}
	if err := json.Unmarshal(batch.PolicySnapshotJSON, &origin); err != nil {
		t.Fatal(err)
	}
	if batch.EntryType != "paper_expansion" || origin.ExpansionID != record.ID || origin.ExpansionVersion != 3 {
		t.Fatalf("origin not persisted: %+v %+v", batch, origin)
	}
	if err := db.Model(&orm.PaperImportItem{}).Where("batch_id=?", batch.ID).Updates(map[string]any{"document_id": "a", "status": "succeeded"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, link := range []orm.AcademicWorkDocument{
		{AcademicWorkID: "a", DatasetID: "papers", DocumentID: "a", SourceProvider: "arxiv", SourceLocator: candidate.URL, SourceVersion: "v2"},
		{AcademicWorkID: "b", DatasetID: "papers", DocumentID: "b", SourceProvider: "upload"},
	} {
		if err := db.Create(&link).Error; err != nil {
			t.Fatal(err)
		}
	}
	var item orm.PaperImportItem
	if err := db.Where("batch_id=?", batch.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		method, itemID := "upload", ""
		if id == "a" {
			method, itemID = "expansion", item.ID
		}
		for _, row := range []any{
			&orm.PaperLibraryEntry{DatasetID: "papers", ID: id, AcademicWorkID: id, Source: "unknown", ImportMethod: method, Revision: 1, TagsJSON: marshal([]string{}), MetadataJSON: marshal(map[string]any{})},
			&orm.PaperLibraryAttachment{DatasetID: "papers", ID: "attachment-" + id, PaperID: id, DocumentID: id, Kind: "pdf", ImportItemID: itemID},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	view, err := paperlibrary.Load(t.Context(), db.DB, "papers", "u", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Data.Attachments) != 2 {
		t.Fatal(view.Data.Attachments)
	}
	for _, item := range view.Data.Attachments {
		if item.AcademicState == nil || item.AcademicState.DocumentID != item.ContentRef {
			t.Fatalf("identity lost: %+v", item)
		}
		if item.ContentRef == "a" && (item.SourceProvider != "arxiv" || item.SourceURL != candidate.URL || item.SourceVersion != "v2" || item.ImportMethod != "expansion" || item.ExpansionID != record.ID) {
			t.Fatalf("source lost in library response: %+v", item)
		}
		if item.ContentRef == "b" && item.ImportMethod != "upload" {
			t.Fatalf("upload mislabeled: %+v", item)
		}
	}
}
