package academic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

func contextWithNoPDF(t *testing.T) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), paperPDFClientKey{}, &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})})
}

func TestBibliographySavesAndQueuesWithoutNetworkAndReplayDoesNotDuplicateJob(t *testing.T) {
	for _, format := range []string{"bib", "ris", "csv"} {
		t.Run(format, func(t *testing.T) {
			db := paperLibraryTestDB(t)
			old := http.DefaultTransport
			http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Host, "crossref") || strings.Contains(r.URL.Host, "arxiv") {
					t.Fatal("network lookup before import response")
				}
				return old.RoundTrip(r)
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			input := createImportRequest{EntryType: "bibliography", TargetDatasetID: "papers", IdempotencyKey: "first", Format: format, Items: []selectedImportItem{{Paper: libraryDraft("p1", "Original title", "10.1234/test")}}}
			for i := 0; i < 2; i++ {
				response := libraryRequest(t, "POST", "/imports", "papers", "owner", input)
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
			}
			var batches []orm.PaperImportBatch
			db.Find(&batches)
			var jobs int64
			db.Model(&orm.AsyncJob{}).Count(&jobs)
			if len(batches) != 1 || batches[0].Status != "pending" || batches[0].AsyncJobID == "" || jobs != 1 {
				t.Fatalf("non-durable/duplicate queue: %+v jobs=%d", batches, jobs)
			}
			if got := readLibrary(t, "papers"); len(got.Data.Papers) != 1 {
				t.Fatal("paper unavailable before enrichment")
			}
		})
	}
}

func TestBibliographyEnrichmentPreservesMetadataAndDoesNotResurrectRemovedPaper(t *testing.T) {
	db := paperLibraryTestDB(t)
	p := libraryDraft("p1", "My edited title", "")
	p.Metadata.Identifiers.Arxiv = "2404.06654"
	p.Metadata.Authors = nil
	importDraft(t, "papers", "first", p)
	var batch orm.PaperImportBatch
	db.First(&batch)
	var item orm.PaperImportItem
	db.Where("batch_id=?", batch.ID).First(&item)
	db.Model(&orm.AsyncJob{}).Where("id=?", batch.AsyncJobID).Updates(map[string]any{"status": "running", "attempt_count": 1})
	job := asyncjob.Job{ID: batch.AsyncJobID, AttemptCount: 1}
	// Candidate replay must not hit metadata providers again.
	item.SelectedCandidateJSON = json.RawMessage(`{"url":"https://arxiv.org/pdf/2404.06654","source_provider":"arxiv"}`)
	if err := enrichBibliographyItem(t.Context(), job, batch, &item, sourceImportCaller(batch)); err != nil {
		t.Fatal(err)
	}
	updated := fillBibliographyMetadata(p.Metadata, WorkInput{Title: "Remote title", Authors: []string{"Author"}, Year: 2000, Venue: "Other", Abstract: "Abstract"})
	if updated.Title != p.Metadata.Title || updated.Year != 2025 || updated.Venue != "Journal" || updated.Abstract != "Abstract" || len(updated.Authors) != 1 {
		t.Fatalf("overwrote imported metadata: %+v", updated)
	}
	db.Model(&orm.PaperLibraryEntry{}).Where("id=?", p.ID).Update("removed_at", "2026-01-01")
	if err := checkEnrichmentMembership(db, batch, item); err == nil {
		t.Fatal("removed membership accepted")
	}
}

func TestBibliographyMetadataProviderFillsOnlyEmptyFields(t *testing.T) {
	db := paperLibraryTestDB(t)
	p := libraryDraft("p1", "Original title", "10.1234/test")
	p.Metadata.Authors = nil
	p.Metadata.Year = 0
	p.Metadata.Venue = "未标注"
	importDraft(t, "papers", "first", p)
	var batch orm.PaperImportBatch
	db.First(&batch)
	var item orm.PaperImportItem
	db.Where("batch_id=?", batch.ID).First(&item)
	db.Model(&orm.AsyncJob{}).Where("id=?", batch.AsyncJobID).Updates(map[string]any{"status": "running", "attempt_count": 1})
	old := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.crossref.org" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"papers","exists":false,"allowed":true}],"source_map":{}}`)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"message":{"DOI":"10.1234/test","title":["Remote title"],"author":[{"given":"Jane","family":"Doe"}],"container-title":["Remote journal"],"issued":{"date-parts":[[2024]]}}}`)), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	prepared, prepareErr := prepareSourceTarget(t.Context(), sourceImportCaller(batch), batch.TargetDatasetID)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	ctx := context.WithValue(prepared, paperPDFClientKey{}, contextWithNoPDF(t).Value(paperPDFClientKey{}))
	err := enrichBibliographyItem(ctx, asyncjob.Job{ID: batch.AsyncJobID, AttemptCount: 1}, batch, &item, sourceImportCaller(batch))
	if err == nil || !strings.Contains(err.Error(), "题录已保存") {
		t.Fatalf("missing PDF must be visible: %v", err)
	}
	var entry orm.PaperLibraryEntry
	db.Where("id=?", p.ID).First(&entry)
	var m paperlibrary.Metadata
	json.Unmarshal(entry.MetadataJSON, &m)
	if m.Title != "Original title" || m.Year != 2024 || len(m.Authors) != 1 || m.Venue != "Remote journal" {
		t.Fatalf("not enriched: %+v; lookup: %v", m, err)
	}
}

func TestBibliographyMetadataOutageStillFindsPDFAndRetainsDiagnostic(t *testing.T) {
	db := paperLibraryTestDB(t)
	importDraft(t, "papers", "first", libraryDraft("p1", "Paper", "10.1234/test"))
	var b orm.PaperImportBatch
	db.First(&b)
	var item orm.PaperImportItem
	db.Where("batch_id=?", b.ID).First(&item)
	db.Model(&orm.AsyncJob{}).Where("id=?", b.AsyncJobID).Updates(map[string]any{"status": "running", "attempt_count": 1})
	old := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	client := &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"best_oa_location":{"url_for_pdf":"https://example.org/paper.pdf"}}`
		if r.URL.Host == "example.org" {
			body = "%PDF-1.7 test"
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	ctx := context.WithValue(t.Context(), paperPDFClientKey{}, client)
	if err := enrichBibliographyItem(ctx, asyncjob.Job{ID: b.AsyncJobID, AttemptCount: 1}, b, &item, sourceImportCaller(b)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(item.SelectedCandidateJSON), "example.org/paper.pdf") || !strings.Contains(string(bibliographyDiagnostics(b, item)), "503") {
		t.Fatal("candidate or provider failure lost")
	}
}

func TestBibliographyTitleMatchingRequiresUnambiguousAuthorAndYear(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	for _, tc := range []struct {
		name, items string
		ok          bool
	}{
		{"exact", `{"DOI":"10.1234/test","title":["A useful paper"],"author":[{"given":"Jane","family":"Doe"}],"issued":{"date-parts":[[2024]]}}`, true},
		{"wrong author", `{"DOI":"10.1234/test","title":["A useful paper"],"author":[{"given":"Other","family":"Author"}],"issued":{"date-parts":[[2024]]}}`, false},
		{"missing result", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":{"items":[` + tc.items + `]}}`)), Request: r}, nil
			})
			doi, err := findBibliographyDOI(t.Context(), paperlibrary.Metadata{Title: "A useful paper", Authors: []string{"Jane Doe"}, Year: 2024})
			if (err == nil) != tc.ok || tc.ok && doi != "10.1234/test" {
				t.Fatalf("doi=%s error=%v", doi, err)
			}
		})
	}
}
