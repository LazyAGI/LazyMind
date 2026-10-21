package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			arxivRequestGate.Lock()
			arxivRequestGate.next = time.Time{}
			arxivRequestGate.Unlock()
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

type importErrorReporter struct{}

func (importErrorReporter) Heartbeat(context.Context) error                 { return nil }
func (importErrorReporter) SetProgress(context.Context, int64, int64) error { return nil }

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
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.AcademicReference{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}); err != nil {
		t.Fatal(err)
	}
	previous, previousLazy, previousState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, previousState)
	t.Cleanup(func() { store.Init(previous, previousLazy, previousState); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	arxivRequestGate.Lock()
	arxivRequestGate.next = time.Time{}
	arxivRequestGate.Unlock()
	return db
}
