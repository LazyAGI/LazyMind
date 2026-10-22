package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

type importErrorTransport func(*http.Request) (*http.Response, error)

func (f importErrorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMetadataResolversExposeProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		lookup func(context.Context) error
	}{
		{"arxiv HTTP", 429, "rate limited", func(ctx context.Context) error {
			_, _, err := resolveWithArxiv(ctx, "A. Author. 2024. A sufficiently long paper title.")
			return err
		}},
		{"arxiv invalid XML", 200, "<html>error</html>", func(ctx context.Context) error {
			_, _, err := resolveWithArxiv(ctx, "A. Author. 2024. A sufficiently long paper title.")
			return err
		}},
		{"arxiv error entry", 200, `<feed><entry><id>http://arxiv.org/api/errors#incorrect_id_format</id><title>Error</title></entry></feed>`, func(ctx context.Context) error {
			_, _, err := resolveWithArxiv(ctx, "A. Author. 2024. A sufficiently long paper title.")
			return err
		}},
		{"openalex malformed", 200, `{broken`, func(ctx context.Context) error {
			_, _, err := resolveWithOpenAlex(ctx, "A. Author. 2024. A sufficiently long paper title.")
			return err
		}},
		{"openalex missing results", 200, `{}`, func(ctx context.Context) error {
			_, _, err := resolveWithOpenAlex(ctx, "A. Author. 2024. A sufficiently long paper title.")
			return err
		}},
		{"unpaywall HTTP", 503, "unavailable", func(ctx context.Context) error {
			_, _, _, err := resolveDOIOpenAccess(ctx, "10.1234/example")
			return err
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			previous := http.DefaultTransport
			http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			if err := test.lookup(context.Background()); err == nil {
				t.Fatal("provider failure was treated as an empty successful lookup")
			}
		})
	}
}

func TestReferenceResolutionReportsProviderFailureBeforeSaving(t *testing.T) {
	db := importErrorDB(t)
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	ref := orm.AcademicReference{ID: "ref-failure", RawText: "A. Author. 2024. A sufficiently long paper title.", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	_, _, _, err := resolveReferenceWithExternal(context.Background(), db, &ref, true)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("provider HTTP failure must propagate, got %v", err)
	}
	var count int64
	if err := db.Model(&orm.AcademicWork{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed resolution created %d placeholder works", count)
	}
}

func TestReferenceResolutionUsesOpenAlexFulltextWithoutAnotherLookup(t *testing.T) {
	for _, location := range []string{
		`"ids":{"arxiv":"https://arxiv.org/abs/2404.06654"}`,
		`"best_oa_location":{"pdf_url":"https://publisher.example/paper.pdf"}`,
	} {
		t.Run(location, func(t *testing.T) {
			db := importErrorDB(t)
			previous := http.DefaultTransport
			http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
				status, body := http.StatusServiceUnavailable, "unavailable"
				if r.URL.Hostname() == "api.openalex.org" {
					status = http.StatusOK
					body = `{"results":[{"id":"https://openalex.org/W123","title":"A sufficiently long paper title","publication_year":2024,` + location + `}]}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			ref := orm.AcademicReference{ID: "ref-fulltext", RawText: "A. Author. 2024. A sufficiently long paper title.", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
			if err := db.Create(&ref).Error; err != nil {
				t.Fatal(err)
			}
			work, _, _, err := resolveReferenceWithExternal(context.Background(), db, &ref, true)
			if err != nil || work.CanonicalTitle != "A sufficiently long paper title" || len(candidatesForWork(work)) == 0 {
				t.Fatalf("usable OpenAlex result was discarded: work=%+v error=%v", work, err)
			}
		})
	}
}

func TestReferenceResolutionStillReportsRequiredArxivLookupFailure(t *testing.T) {
	db := importErrorDB(t)
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusServiceUnavailable, "unavailable"
		if r.URL.Hostname() == "api.openalex.org" {
			status, body = http.StatusOK, `{"results":[]}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	ref := orm.AcademicReference{RawText: "A. Author. 2024. A sufficiently long paper title."}
	if _, _, _, err := resolveReferenceWithExternal(context.Background(), db, &ref, true); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("required arXiv failure must propagate: %v", err)
	}
}

type importErrorReporter struct{}

func (importErrorReporter) Heartbeat(context.Context) error                 { return nil }
func (importErrorReporter) SetProgress(context.Context, int64, int64) error { return nil }

type cancelImportReporter struct{ cancel context.CancelFunc }

func (r cancelImportReporter) Heartbeat(ctx context.Context) error {
	r.cancel()
	return ctx.Err()
}
func (cancelImportReporter) SetProgress(context.Context, int64, int64) error { return nil }

func TestInterruptedPaperImportPersistsFailureAndCanBeRetried(t *testing.T) {
	for _, mode := range []string{"status_write_failed", "context_canceled"} {
		t.Run(mode, func(t *testing.T) {
			db := importErrorDB(t)
			if err := db.AutoMigrate(&orm.Document{}); err != nil {
				t.Fatal(err)
			}
			batch := orm.PaperImportBatch{ID: "batch-interrupted", TargetDatasetID: "dataset", Status: "pending", TotalItems: 3, SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)}
			work := orm.AcademicWork{ID: "work-interrupted", CanonicalTitle: "Paper", NormalizedTitle: "paper", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
			for _, row := range []any{&batch, &work} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []string{"completed", "first", "second"} {
				status := "pending"
				if id == "completed" {
					status = "succeeded"
				}
				item := orm.PaperImportItem{ID: id, BatchID: batch.ID, AcademicWorkID: work.ID, Status: status, SelectedCandidateJSON: json.RawMessage(`{"url":"file:///unavailable.pdf"}`), ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`)}
				if err := db.Create(&item).Error; err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var reporter asyncjob.Reporter = importErrorReporter{}
			if mode == "context_canceled" {
				reporter = cancelImportReporter{cancel: cancel}
			} else {
				if err := db.Callback().Update().Before("gorm:update").Register("fail_import_stage", func(tx *gorm.DB) {
					updates, ok := tx.Statement.Dest.(map[string]any)
					if ok && tx.Statement.Table == "paper_import_items" && updates["stage"] == "acquire" {
						tx.AddError(errors.New("status write unavailable"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			job := asyncjob.Job{PayloadJSON: json.RawMessage(`{"batch_id":"batch-interrupted","user_id":"user"}`)}
			if _, err := handlePaperImportJob(ctx, job, reporter); err == nil {
				t.Fatal("interruption must return the original error")
			}
			var items []orm.PaperImportItem
			if err := db.Order("id").Find(&items).Error; err != nil {
				t.Fatal(err)
			}
			if len(items) != 3 || items[0].Status != "succeeded" || items[1].Status != "failed" || items[2].Status != "failed" || items[1].ErrorCode == "" || string(items[1].ErrorDetailsJSON) == "{}" {
				t.Fatalf("interruption left invisible or unretryable items: %+v", items)
			}
			if err := db.Take(&batch, "id = ?", batch.ID).Error; err != nil {
				t.Fatal(err)
			}
			if batch.Status != "completed_with_errors" || batch.FailedItems != 2 || batch.CompletedItems != 1 {
				t.Fatalf("batch did not report the interruption: %+v", batch)
			}
			if mode == "status_write_failed" {
				if err := db.Callback().Update().Remove("fail_import_stage"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := handlePaperImportJob(context.Background(), job, importErrorReporter{}); err == nil {
				t.Fatal("retry must process the interrupted items and expose their download failures")
			}
			var item orm.PaperImportItem
			if err := db.Take(&item, "id = ?", "first").Error; err != nil {
				t.Fatal(err)
			}
			if item.ErrorCode != "download_failed" {
				t.Fatalf("interrupted item was not retried: %+v", item)
			}
		})
	}
}

func TestPaperImportRejectsProcessingFailureEvenWhenDocumentExists(t *testing.T) {
	want := errors.New("processing service unavailable")
	if got := validatePaperImportResult(doc.LocalFileImportResult{DocumentID: "saved-document"}, want); !errors.Is(got, want) {
		t.Fatalf("processing error was hidden behind document ID: %v", got)
	}
	if err := validatePaperImportResult(doc.LocalFileImportResult{}, nil); err == nil {
		t.Fatal("missing document must not be considered a successful import")
	}
}

func TestListImportsPreservesFailureWhenEquivalentWorkWasImportedLater(t *testing.T) {
	db := importErrorDB(t)
	batch := orm.PaperImportBatch{ID: "batch-history", TargetDatasetID: "dataset", CreatedBy: "user", Status: "failed", SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)}
	work := orm.AcademicWork{ID: "work-history", CanonicalTitle: "Paper", NormalizedTitle: "paper", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	item := orm.PaperImportItem{ID: "item-history", BatchID: batch.ID, AcademicWorkID: work.ID, Status: "failed", ErrorCode: "download_failed", SelectedCandidateJSON: json.RawMessage(`{}`), ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{"message":"paper download returned HTTP 503"}`)}
	link := orm.AcademicWorkDocument{AcademicWorkID: work.ID, DatasetID: "dataset", DocumentID: "later-document"}
	for _, row := range []any{&batch, &work, &item, &link} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/academic/imports", nil)
	req.Header.Set("X-User-Id", "user")
	response := httptest.NewRecorder()
	ListImports(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Items []ImportQueueItem `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Status != "failed" || body.Items[0].ErrorCode != "download_failed" || !strings.Contains(body.Items[0].ErrorMessage, "503") {
		t.Fatalf("failed history was hidden: %s", response.Body.String())
	}
}

func TestPaperImportJobReturnsFailureForFailedItems(t *testing.T) {
	db := importErrorDB(t)
	batch := orm.PaperImportBatch{ID: "batch-failure", TargetDatasetID: "dataset", Status: "pending", SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)}
	work := orm.AcademicWork{ID: "work-failure", CanonicalTitle: "Paper", NormalizedTitle: "paper", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	item := orm.PaperImportItem{ID: "item-failure", BatchID: batch.ID, AcademicWorkID: work.ID, Status: "pending", SelectedCandidateJSON: json.RawMessage(`{"url":"file:///unavailable.pdf"}`), ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`)}
	for _, row := range []any{&batch, &work, &item} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := handlePaperImportJob(context.Background(), asyncjob.Job{PayloadJSON: json.RawMessage(`{"batch_id":"batch-failure","user_id":"user"}`)}, importErrorReporter{})
	if err == nil {
		t.Fatal("failed item must not report async job success")
	}
	if result.ErrorCode == "" || !result.Permanent {
		t.Fatalf("failed item must expose error and await explicit retry: %#v", result)
	}
	if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != "failed" || item.ErrorCode != "download_failed" {
		t.Fatalf("lost import failure: %#v", item)
	}
}

func importErrorDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "imports.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.AcademicDocumentState{}, &orm.AcademicReference{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}); err != nil {
		t.Fatal(err)
	}
	previous, previousLazy, previousState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, previousState)
	t.Cleanup(func() { store.Init(previous, previousLazy, previousState); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}
