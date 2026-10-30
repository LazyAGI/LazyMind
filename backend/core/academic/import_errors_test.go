package academic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
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
			installTestArxivClient(t)
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
	installTestArxivClient(t)
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
			installTestArxivClient(t)
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
	installTestArxivClient(t)
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

func importErrorDB(t *testing.T, databases ...*gorm.DB) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	if len(databases) > 0 {
		db = databases[0]
	} else {
		var err error
		db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "imports.db")), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.AcademicDocumentState{}, &orm.AcademicReference{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}, &orm.PaperLibraryEntry{}, &orm.AcademicExpansion{}, &orm.Document{}, &orm.Task{}, &orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	previous, previousLazy, previousState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, previousState)
	t.Cleanup(func() { store.Init(previous, previousLazy, previousState); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}
