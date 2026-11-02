package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type cacheFixture struct {
	db      *gorm.DB
	acquire func(context.Context, FulltextCandidate) (string, int64, error)
}

func cacheTestFixture(t *testing.T) (cacheFixture, *atomic.Int32) {
	t.Helper()
	db := orm.OpenTestDB(t).DB
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	count := &atomic.Int32{}
	return cacheFixture{db: db, acquire: func(_ context.Context, c FulltextCandidate) (string, int64, error) {
		count.Add(1)
		body := []byte("%PDF-1.7\nidentical PDF bytes across versions\n%%EOF")
		p := filepath.Join(t.TempDir(), "paper.pdf")
		return p, int64(len(body)), os.WriteFile(p, body, 0600)
	}}, count
}
func importPipelineFixture(t *testing.T) (*gorm.DB, orm.PaperImportBatch, orm.PaperImportItem, *doc.OriginalService) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.Dataset{}, &orm.Task{}, &orm.ACLModel{}, &orm.Document{}, &orm.UploadedFile{}, &orm.DocumentProcessingState{}, &orm.AcademicDocumentState{}, &orm.AcademicReference{}, &orm.UserUIPreferences{}, &orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}, &orm.AsyncJob{}).DB
	previous, lazy, state := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, state)
	t.Cleanup(func() { store.Init(previous, lazy, state) })
	transport := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"target","exists":false,"allowed":true}],"source_map":{}}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	b := orm.PaperImportBatch{ID: "batch", EntryType: "paper_link", AsyncJobID: "job", CreatedBy: "alice", TargetDatasetID: "target", Status: "running", PolicySnapshotJSON: marshal(map[string]string{}), SourceDocumentIDsJSON: marshal([]string{})}
	ref := testVerifiedPaper()
	ref.PaperID = "work"
	item := orm.PaperImportItem{ID: "item", BatchID: b.ID, AcademicWorkID: "work", Status: "running", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]string{}), ErrorDetailsJSON: marshal(map[string]string{}), SelectedCandidateJSON: marshal(savedImportCandidate{FulltextCandidate: testPDFCandidate(), VerifiedPaper: &ref})}
	for _, r := range []any{&orm.Dataset{ID: "target", Scene: "general", ProcessingLevel: "stored", Ext: marshal(map[string]string{}), BaseModel: orm.BaseModel{CreateUserID: "alice"}}, &orm.AcademicWork{ID: "work", ArxivIDBase: ref.ArxivID, CanonicalTitle: ref.Title, AuthorsJSON: marshal(ref.Authors), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]string{})}, &orm.AsyncJob{ID: "job", Status: "running", AttemptCount: 1, PayloadJSON: marshal(map[string]string{})}, &b, &item} {
		if err := db.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, SubmitProcessing: func(context.Context, doc.DatasetCatalogCaller, string, string) (doc.StartTaskResult, error) {
		return doc.StartTaskResult{Status: "STARTED", SubmitStatus: "ACCEPTED"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return db, b, item, svc
}
func seedPipelineCache(t *testing.T, db *gorm.DB, version string) {
	t.Helper()
	ref := testVerifiedPaper()
	ref.SourceVersion = version
	ref.PDFURL = "https://arxiv.org/pdf/" + ref.ArxivID + version
	guard, err := lockArxivImport(t.Context(), db, ref.ArxivID)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.release()
	_, _, err = acquireCachedPDFLocked(guard.ctx, "alice", ref, func(_ context.Context, c FulltextCandidate) (string, int64, error) {
		p := filepath.Join(t.TempDir(), "download.pdf")
		body := []byte("%PDF-1.7\nidentical PDF bytes across versions\n%%EOF")
		return p, int64(len(body)), os.WriteFile(p, body, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestUnifiedPaperImportReusesCacheAndKeepsDifferentVersions(t *testing.T) {
	db, b, item, svc := importPipelineFixture(t)
	job := asyncjob.Job{ID: "job", AttemptCount: 1}
	for i, version := range []string{"v2", "v2", "v3"} {
		seedPipelineCache(t, db, version)
		if i > 0 {
			item.ID = version + "-repeat"
			item.DocumentID = ""
			item.DocumentTaskID = ""
			ref := testVerifiedPaper()
			ref.SourceVersion = version
			ref.PDFURL = "https://arxiv.org/pdf/" + ref.ArxivID + version
			c := testPDFCandidate()
			c.Version = version
			c.URL = ref.PDFURL
			item.SelectedCandidateJSON = marshal(savedImportCandidate{FulltextCandidate: c, VerifiedPaper: &ref})
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := processImportItem(t.Context(), job, b, item, sourceImportCaller(b), svc); err != nil {
			t.Fatal(err)
		}
		var saved orm.PaperImportItem
		db.Where("id=?", item.ID).Take(&saved)
		want := "succeeded"
		if i == 1 {
			want = "skipped"
		}
		if saved.Status != want || saved.DocumentID == "" {
			t.Fatalf("bad import receipt: %+v", saved)
		}
	}
	var docs, tasks, links int64
	db.Model(&orm.Document{}).Count(&docs)
	db.Model(&orm.Task{}).Count(&tasks)
	db.Model(&orm.AcademicWorkDocument{}).Count(&links)
	if docs != 2 || tasks != 2 || links != 2 {
		t.Fatalf("version dedup: docs=%d tasks=%d links=%d", docs, tasks, links)
	}
}
func TestUnifiedImportProcessingFailureAndCancellationRetainOneBinding(t *testing.T) {
	for _, cancelAfterCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "submission_failure", true: "cancellation"}[cancelAfterCommit], func(t *testing.T) {
			db, b, item, _ := importPipelineFixture(t)
			seedPipelineCache(t, db, "v2")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			submits := 0
			svc, _ := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, SubmitProcessing: func(context.Context, doc.DatasetCatalogCaller, string, string) (doc.StartTaskResult, error) {
				submits++
				if submits == 1 {
					if cancelAfterCommit {
						cancel()
					}
					return doc.StartTaskResult{}, errors.New("processing temporarily unavailable")
				}
				return doc.StartTaskResult{Status: "STARTED", SubmitStatus: "ACCEPTED"}, nil
			}})
			if err := processImportItem(ctx, asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err == nil {
				t.Fatal("submission failure hidden")
			}
			db.Where("id=?", item.ID).Take(&item)
			if item.DocumentID == "" || item.DocumentTaskID == "" || item.ContentSHA256 == "" {
				t.Fatalf("committed binding lost: %+v", item)
			}
			if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err != nil {
				t.Fatal(err)
			}
			var docs, tasks int64
			db.Model(&orm.Document{}).Count(&docs)
			db.Model(&orm.Task{}).Count(&tasks)
			if docs != 1 || tasks != 1 || submits != 2 {
				t.Fatal(docs, tasks, submits)
			}
		})
	}
}
func TestUnifiedImportAssociationFailureRollsBackDocumentAndReceipt(t *testing.T) {
	db, b, item, svc := importPipelineFixture(t)
	seedPipelineCache(t, db, "v2")
	db.Callback().Create().Before("gorm:create").Register("fail-association", func(tx *gorm.DB) {
		if tx.Statement.Table == "academic_work_documents" {
			tx.AddError(errors.New("association unavailable"))
		}
	})
	if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err == nil {
		t.Fatal("association failure hidden")
	}
	var docs int64
	db.Model(&orm.Document{}).Count(&docs)
	db.Where("id=?", item.ID).Take(&item)
	if docs != 0 || item.DocumentID != "" {
		t.Fatalf("partial binding committed: %d %+v", docs, item)
	}
	db.Callback().Create().Remove("fail-association")
	if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
}
func TestUnifiedBatchInterruptionPersistsRetryableFailure(t *testing.T) {
	db, b, _, _ := importPipelineFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	_, err := handleImportBatchJob(ctx, asyncjob.Job{ID: "job", AttemptCount: 1}, cancelImportReporter{cancel: cancel}, b)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var item orm.PaperImportItem
	db.Where("batch_id=?", b.ID).Take(&item)
	if item.Status != "failed" || item.ErrorCode != "import_interrupted" {
		t.Fatalf("invisible interruption: %+v", item)
	}
}

func TestUnifiedConcurrentImportsPublishOneVersionedDocument(t *testing.T) {
	db, b, item, svc := importPipelineFixture(t)
	seedPipelineCache(t, db, "v2")
	pool, _ := db.DB()
	pool.SetMaxOpenConns(1)
	other := b
	other.ID = "other-batch"
	other.AsyncJobID = "other-job"
	second := item
	second.ID = "other-item"
	second.BatchID = other.ID
	for _, row := range []any{&orm.AsyncJob{ID: other.AsyncJobID, Status: "running", AttemptCount: 1, PayloadJSON: marshal(map[string]string{})}, &other, &second} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 2)
	for _, pair := range []struct {
		batch orm.PaperImportBatch
		item  orm.PaperImportItem
	}{{b, item}, {other, second}} {
		go func() {
			done <- processImportItem(t.Context(), asyncjob.Job{ID: pair.batch.AsyncJobID, AttemptCount: 1}, pair.batch, pair.item, sourceImportCaller(pair.batch), svc)
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var docs, tasks int64
	db.Model(&orm.Document{}).Count(&docs)
	db.Model(&orm.Task{}).Count(&tasks)
	if docs != 1 || tasks != 1 {
		t.Fatal("concurrent imports duplicated binding", docs, tasks)
	}
}
func TestUnifiedImportPinsMetadataBeforeBindingAndReusesItAfterFailure(t *testing.T) {
	db, b, item, svc := importPipelineFixture(t)
	seedPipelineCache(t, db, "v2")
	item.SelectedCandidateJSON = marshal(FulltextCandidate{URL: "https://arxiv.org/pdf/2404.06654", SourceProvider: "arxiv", SourceType: "official_pdf"})
	db.Model(&item).Update("selected_candidate_json", item.SelectedCandidateJSON)
	previous := http.DefaultTransport
	queries := 0
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "export.arxiv.org" {
			return previous.RoundTrip(r)
		}
		queries++
		xml := `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>https://arxiv.org/abs/2404.06654v2</id><title>Version two</title><summary>Original abstract</summary><published>2024-01-01T00:00:00Z</published><author><name>Author</name></author></entry></feed>`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(xml)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	db.Callback().Create().Before("gorm:create").Register("fail-pinned-association", func(tx *gorm.DB) {
		if tx.Statement.Table == "academic_work_documents" {
			tx.AddError(errors.New("association temporarily unavailable"))
		}
	})
	if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err == nil {
		t.Fatal("expected association failure")
	}
	db.Where("id=?", item.ID).Take(&item)
	var saved savedImportCandidate
	if err := json.Unmarshal(item.SelectedCandidateJSON, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != "v2" || saved.VerifiedPaper == nil || saved.VerifiedPaper.Title != "Version two" {
		t.Fatalf("lost pinned input: %+v", saved)
	}
	db.Callback().Create().Remove("fail-pinned-association")
	if err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatal("retry queried mutable metadata again", queries)
	}
}

func TestUnifiedImportRejectsMismatchedPaperIdentityBeforeDownloading(t *testing.T) {
	db, b, item, svc := importPipelineFixture(t)
	if err := db.Model(&orm.AcademicWork{}).Where("id=?", item.AcademicWorkID).Update("arxiv_id_base", "2404.06655").Error; err != nil {
		t.Fatal(err)
	}
	err := processImportItem(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, sourceImportCaller(b), svc)
	if err == nil || !strings.Contains(err.Error(), "identity conflict") {
		t.Fatalf("mismatched candidate accepted: %v", err)
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 0 {
		t.Fatal("identity mismatch published a document")
	}
}
