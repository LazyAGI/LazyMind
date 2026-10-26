package academic

import (
	"context"
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
	"lazymind/core/academic/arxivsource"
	"lazymind/core/common/orm"
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
		if r.URL.Hostname() == "export.arxiv.org" {
			calls++
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: http.Header{}, Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
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

func TestFulltextPDFIsReusedByImportWithoutMetadataLookup(t *testing.T) {
	svc, downloads := arxivImportTestService(t)
	ref := testVerifiedPaper()
	work, _, err := findOrCreateWork(t.Context(), svc.db, arxivWorkInput(arxivsource.Metadata{Title: ref.Title, BaseID: ref.ArxivID, Version: ref.SourceVersion, PDFURL: ref.PDFURL}))
	if err != nil {
		t.Fatal(err)
	}
	ref.PaperID = work.ID
	guard, err := lockArxivImport(t.Context(), svc.db, ref.ArxivID)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := acquireCachedPDFLocked(guard.ctx, "user", ref, svc.acquire)
	guard.release()
	if err != nil {
		t.Fatal(err)
	}
	batch := orm.PaperImportBatch{ID: "reuse-batch", CreatedBy: "user", TargetDatasetID: "dataset", Status: "pending", SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]string{})}
	item := orm.PaperImportItem{ID: "reuse-item", BatchID: batch.ID, AcademicWorkID: work.ID, Status: "pending", SelectedCandidateJSON: marshal(savedImportCandidate{FulltextCandidate: testPDFCandidate(), VerifiedPaper: &ref}), ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]string{}), ErrorDetailsJSON: marshal(map[string]string{})}
	if err = svc.db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("limited")), Header: http.Header{}, Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	service := paperImportService{acquire: svc.acquire, importFile: svc.importFile}
	if _, err = service.handleItem(t.Context(), &batch, &item, paperImportPayload{UserID: "user"}, importErrorReporter{}, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Take(&item, "id=?", item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != "succeeded" || calls != 0 || downloads.Load() != 1 {
		t.Fatalf("redundant network or incomplete import: %+v metadata=%d downloads=%d", item, calls, downloads.Load())
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("import removed the shared temporary PDF", err)
	}
	svc.lookup = func(context.Context, string) (arxivsource.Metadata, error) {
		t.Fatal("existing attachment queried metadata")
		return arxivsource.Metadata{}, nil
	}
	if _, err = svc.importPaper(t.Context(), ref.PDFURL, "dataset", "user", ""); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatal("existing attachment downloaded again")
	}
}

func TestPDFCacheIsolationCorruptionAndNightlyCleanup(t *testing.T) {
	svc, downloads := arxivImportTestService(t)
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

func TestLegacyVerifiedImportRecoveryStaysWithinUserCollectionAndVersion(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	ref := testVerifiedPaper()
	record := orm.AcademicExpansion{ID: "old-expansion", UserID: "user", DatasetID: "dataset", DiscoveryMode: "external", DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{ref}}}), SeedSnapshotJSON: marshal([]paperSource{}), ProgressJSON: marshal(map[string]string{}), ResultJSON: marshal(ReviewWorkflowInput{}), ConversationID: "old-chat", RequestKey: "old", RequestOwner: "user"}
	if err := svc.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, dataset, version string
		found                  bool
	}{{"user", "dataset", "v2", true}, {"user", "other", "v2", false}, {"other", "dataset", "v2", false}, {"user", "dataset", "v1", false}} {
		candidate := testPDFCandidate()
		candidate.Version = tc.version
		found, err := verifiedImportPaper(t.Context(), svc.db, tc.user, tc.dataset, "a", candidate)
		if err != nil || (found != nil) != tc.found {
			t.Fatal(tc, found, err)
		}
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
	if err := db.Create(&orm.Document{ID: "legacy", DatasetID: "papers", DisplayName: "legacy.pdf", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicDocumentState{DocumentID: "legacy", DatasetID: "papers", Kind: "paper", PaperID: "a", Status: "succeeded"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: "a", DatasetID: "papers", DocumentID: "legacy", SourceProvider: "arxiv"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.PaperImportBatch{ID: "legacy-batch", TargetDatasetID: "papers", EntryType: "single_reference", SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{})}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.PaperImportItem{ID: "legacy-item", BatchID: "legacy-batch", AcademicWorkID: "a", DocumentID: "legacy", Status: "succeeded", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{})}).Error; err != nil {
		t.Fatal(err)
	}

	response = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/?dataset_id=papers&document_ids=a,b,legacy", nil)
	req.Header.Set("X-User-Id", "u")
	DocumentStates(response, req)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var result struct {
		Items []struct {
			State          orm.AcademicDocumentState `json:"state"`
			SourceProvider string                    `json:"source_provider"`
			SourceURL      string                    `json:"source_url"`
			SourceVersion  string                    `json:"source_version"`
			ImportMethod   string                    `json:"import_method"`
			ExpansionID    string                    `json:"expansion_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 {
		t.Fatal(response.Body.String())
	}
	for _, item := range result.Items {
		if item.State.DocumentID == "a" && (item.SourceProvider != "arxiv" || item.SourceURL != candidate.URL || item.SourceVersion != "v2" || item.ImportMethod != "expansion" || item.ExpansionID != record.ID) {
			t.Fatalf("source lost in knowledge response: %+v", item)
		}
		if item.State.DocumentID == "b" && item.ImportMethod != "upload" {
			t.Fatalf("upload mislabeled: %+v", item)
		}
		if item.State.DocumentID == "legacy" && item.ImportMethod != "" {
			t.Fatalf("invented provenance for a legacy expansion import: %+v", item)
		}

	}
}
