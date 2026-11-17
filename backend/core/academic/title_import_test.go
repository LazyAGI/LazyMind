package academic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func TestTitleImportParallelProvidersWithoutKey(t *testing.T) {
	paperFixture(t)
	started := make(chan string, 2)
	release := make(chan struct{})
	go func() { <-started; <-started; close(release) }()
	mockPaperTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.openalex.org" && r.URL.Host != "api.crossref.org" {
			return nil, fmt.Errorf("unexpected host %s", r.URL.Host)
		}
		if r.URL.Query().Get("api_key") != "" {
			return nil, errors.New("unexpected API key")
		}
		started <- r.URL.Host
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		if r.URL.Host == "api.openalex.org" {
			return paperTestResponse(r, 200, `{"results":[{"title":"A useful paper","doi":"https://doi.org/10.1234/test","publication_year":2024}]}`), nil
		}
		return paperTestResponse(r, 200, `{"message":{"items":[{"title":["A useful paper"],"DOI":"10.1234/test","author":[{"given":"Jane","family":"Doe"}],"issued":{"date-parts":[[2024]]}}]}}`), nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	work, err := resolveSelectedTitle(ctx, "alice", capability.PaperSelection{Title: "A useful paper"})
	if err != nil || work.DOI != "10.1234/test" {
		t.Fatalf("concurrent merged title match: %+v %v", work, err)
	}
}

func TestTitleImportProviderFailureAndAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name, oa, cr string
		oaStatus     int
		want         string
	}{
		{"supplement is not another paper", `{"results":[]}`, `{"message":{"items":[{"title":["A useful paper"],"DOI":"10.1234/test","type":"journal-article"},{"title":["A useful paper"],"DOI":"10.1234/test.s001","type":"component"}]}}`, 200, "10.1234/test"},
		{"same-title chapter is not the requested paper", `{"results":[]}`, `{"message":{"items":[{"title":["A useful paper"],"DOI":"10.1234/chapter","type":"book-chapter"}]}}`, 200, ""},
		{"openalex chapter is not the requested paper", `{"results":[{"title":"A useful paper","doi":"10.1234/chapter","type":"book-chapter"}]}`, `{"message":{"items":[]}}`, 200, ""},
		{"chapter classification survives duplicate provider record", `{"results":[{"title":"A useful paper","doi":"10.1234/chapter"}]}`, `{"message":{"items":[{"title":["A useful paper"],"DOI":"10.1234/chapter","type":"book-chapter"}]}}`, 200, ""},
		{"crossref only", `{}`, `{"message":{"items":[{"title":["A useful paper"],"DOI":"10.1234/test"}]}}`, 429, "10.1234/test"},
		{"different identities", `{"results":[{"title":"A useful paper","doi":"10.1234/other"}]}`, `{"message":{"items":[{"title":["A useful paper"],"DOI":"10.1234/test"}]}}`, 200, ""},
		{"no exact title", `{"results":[]}`, `{"message":{"items":[{"title":["A different paper"],"DOI":"10.1234/test"}]}}`, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paperFixture(t)
			mockPaperTransport(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "api.openalex.org" {
					return paperTestResponse(r, tc.oaStatus, tc.oa), nil
				}
				return paperTestResponse(r, 200, tc.cr), nil
			})
			w, e := resolveSelectedTitle(t.Context(), "alice", capability.PaperSelection{Title: "A useful paper"})
			if tc.want != "" {
				if e != nil || w.DOI != tc.want {
					t.Fatalf("%+v %v", w, e)
				}
			} else if !errors.Is(e, errPaperSelectionUnconfirmed) {
				t.Fatalf("ambiguous/unmatched must not import: %+v %v", w, e)
			}
		})
	}
}

func TestAgentTitleImportConfirmsBeforeSavingAndDoesNotDownloadPDF(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	var searches atomic.Int32
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.crossref.org" {
			searches.Add(1)
			return paperTestResponse(r, 200, `{"message":{"items":[{"title":["A useful paper"],"DOI":"10.1234/test","author":[{"given":"Jane","family":"Doe"}],"issued":{"date-parts":[[2024]]}}]}}`), nil
		}
		if r.URL.Host == "api.openalex.org" {
			return paperTestResponse(r, 200, `{"results":[]}`), nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "title", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	result, e := AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{Title: "A useful paper", DOI: " ", WorkID: " ", ArxivID: " "}}}, op)
	if e != nil || result.BatchID == "" || searches.Load() != 1 {
		t.Fatalf("title must be resolved before receipt: %+v %v searches=%d", result, e, searches.Load())
	}
	// A retry of the accepted operation must not depend on providers being online.
	before := searches.Load()
	again, replayErr := AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{Title: "A useful paper", DOI: " ", WorkID: " ", ArxivID: " "}}}, op)
	if replayErr != nil || again.BatchID != result.BatchID || searches.Load() != before {
		t.Fatalf("replay searched again: %+v %v searches=%d", again, replayErr, searches.Load())
	}
	var work orm.AcademicWork
	db.First(&work)
	if work.DOINormalized != "10.1234/test" || work.PublicationYear != 2024 {
		t.Fatalf("unconfirmed metadata saved: %+v", work)
	}
	var item orm.PaperImportItem
	db.Where("batch_id=?", result.BatchID).First(&item)
	if item.DocumentID != "" {
		t.Fatal("PDF work must remain asynchronous")
	}
}

func TestAgentTitleImportNoMatchDoesNotCreateEntry(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "missing", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	result, err := AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{Title: "No matching paper"}}}, op)
	if err != nil || result.BatchID != "" || len(result.Items) != 1 || result.Items[0].Status != "needs_confirmation" {
		t.Fatalf("unexpected receipt %+v %v", result, err)
	}
	var n int64
	db.Model(&orm.PaperLibraryEntry{}).Count(&n)
	if n != 0 {
		t.Fatal("unmatched title inserted")
	}
}

func TestAgentTitleImportPartialReplayDoesNotResolvePreviouslyUnmatchedItem(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	var secondAvailable atomic.Bool
	var calls atomic.Int32
	old := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.crossref.org" {
			return old.RoundTrip(r)
		}
		calls.Add(1)
		title := r.URL.Query().Get("query.title")
		if title == "Second paper" && !secondAvailable.Load() {
			return paperTestResponse(r, 200, `{"message":{"items":[]}}`), nil
		}
		return paperTestResponse(r, 200, fmt.Sprintf(`{"message":{"items":[{"title":[%q],"DOI":%q}]}}`, title, "10.1234/"+map[string]string{"First paper": "first", "Second paper": "second"}[title])), nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "partial", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	in := capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{Title: "First paper"}, {Title: "Second paper"}, {Title: "First paper"}}}
	first, err := AgentImportPapers(t.Context(), call, in, op)
	if err != nil || first.BatchID == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	secondAvailable.Store(true)
	before := calls.Load()
	replay, err := AgentImportPapers(t.Context(), call, in, op)
	if err != nil || replay.BatchID != first.BatchID || calls.Load() != before || replay.Items[0].Status != first.Items[0].Status || replay.Items[1].Status != first.Items[1].Status || replay.Items[2].Status != first.Items[2].Status {
		t.Fatalf("unstable partial replay: %+v %v calls %d -> %d", replay, err, before, calls.Load())
	}
	var n int64
	db.Model(&orm.PaperLibraryEntry{}).Count(&n)
	if n != 1 {
		t.Fatalf("replay added membership: %d", n)
	}
}
