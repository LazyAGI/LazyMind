package academic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func citationLookupTransport(t *testing.T, handler func(*http.Request) (int, string)) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		status, body := handler(r)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func TestCitationImportPrefersExactDOIMetadataOverTitleSearch(t *testing.T) {
	db := importErrorDB(t)
	citationLookupTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host != "api.crossref.org" || !strings.HasSuffix(r.URL.Path, "/10.1234/exact") {
			t.Fatalf("unexpected provider lookup: %s", r.URL)
		}
		return 200, `{"message":{"DOI":"10.1234/exact","title":["The Verified Paper"],"author":[{"given":"Ada","family":"Lovelace"}],"issued":{"date-parts":[[2024]]}}}`
	})
	ref := orm.AcademicReference{ID: "ref-doi", RawText: "A. Other. 2024. A different title. doi:10.1234/exact", DOINormalized: "10.1234/exact", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	work, input, err := resolveTestCitation(context.Background(), db, &ref)
	if err != nil || work.DOINormalized != "10.1234/exact" || input.Title != "The Verified Paper" || len(input.Authors) != 1 || input.Authors[0] != "Ada Lovelace" {
		t.Fatalf("DOI was not checked with Crossref: work=%+v input=%+v error=%v", work, input, err)
	}
}

func TestCitationImportReadsDOIFromRawReferenceWhenStoredFieldIsEmpty(t *testing.T) {
	citationLookupTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host != "api.crossref.org" || !strings.HasSuffix(r.URL.Path, "/10.1234/exact") {
			t.Fatalf("unexpected provider lookup: %s", r.URL)
		}
		return 200, `{"message":{"DOI":"10.1234/exact","title":["The Verified Paper"]}}`
	})
	input, err := verifiedCitationInput(t.Context(), &orm.AcademicReference{RawText: "A. Author. The Verified Paper. doi:10.1234/exact"})
	if err != nil || input.DOI != "10.1234/exact" || input.Title != "The Verified Paper" {
		t.Fatalf("DOI in original reference was not verified: %+v %v", input, err)
	}
}

func TestCitationImportReplacesUnattachedRawReferenceTitle(t *testing.T) {
	db := importErrorDB(t)
	citationLookupTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host != "api.crossref.org" {
			t.Fatalf("unexpected provider lookup: %s", r.URL)
		}
		return 200, `{"message":{"DOI":"10.1234/exact","title":["The Verified Paper"],"author":[{"given":"Ada","family":"Lovelace"}],"issued":{"date-parts":[[2024]]}}}`
	})
	raw := "A. Other. 2024. The rough reference string. doi:10.1234/exact"
	old := orm.AcademicWork{ID: "old-citation-work", CanonicalTitle: raw, NormalizedTitle: NormalizeTitle(raw), DOINormalized: "10.1234/exact", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	ref := orm.AcademicReference{ID: "ref-old", RawText: raw, DOINormalized: "10.1234/exact", ResolvedWorkID: old.ID, AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	work, _, err := resolveTestCitation(context.Background(), db, &ref)
	if err != nil || work.CanonicalTitle != "The Verified Paper" || work.NormalizedTitle != NormalizeTitle("The Verified Paper") {
		t.Fatalf("unattached raw reference title was not replaced: work=%+v error=%v", work, err)
	}
}

func TestCitationImportRejectsUnmatchedReferenceInsteadOfSavingRawText(t *testing.T) {
	db := importErrorDB(t)
	citationLookupTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host == "api.openalex.org" {
			return 200, `{"results":[]}`
		}
		if r.URL.Host == "export.arxiv.org" {
			return 200, `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`
		}
		t.Fatalf("unexpected provider lookup: %s", r.URL)
		return 500, ""
	})
	ref := orm.AcademicReference{ID: "ref-unmatched", RawText: "A. Author. 2024. A sufficiently long paper title.", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveTestCitation(context.Background(), db, &ref); err == nil {
		t.Fatal("unmatched reference should not become a library paper")
	}
	var count int64
	if err := db.Model(&orm.AcademicWork{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unmatched reference created %d works: %v", count, err)
	}
}

func TestCitationImportRejectsApproximateOpenAlexTitle(t *testing.T) {
	db := importErrorDB(t)
	citationLookupTransport(t, func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "api.openalex.org":
			return 200, `{"results":[{"id":"https://openalex.org/W123","title":"A sufficiently long paper title with an extra claim","publication_year":2024,"doi":"https://doi.org/10.1234/other","best_oa_location":{"pdf_url":"https://publisher.example/other.pdf"}}]}`
		case "export.arxiv.org":
			return 200, `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`
		}
		t.Fatalf("unexpected provider lookup: %s", r.URL)
		return 500, ""
	})
	ref := orm.AcademicReference{ID: "ref-near-title", RawText: "A. Author. 2024. A sufficiently long paper title.", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveTestCitation(context.Background(), db, &ref); err == nil {
		t.Fatal("title overlap alone should not authorize import")
	}
}

func TestCitationPreviewLooksUpOneReferenceAndReturnsImportReceipt(t *testing.T) {
	db := paperFixture(t)
	citationLookupTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host == "scan-control-plane:18080" {
			return 404, `{}`
		}
		if r.URL.Host != "export.arxiv.org" {
			t.Fatalf("unexpected provider lookup: %s", r.URL)
		}
		return 200, fallbackArxivMatch
	})
	ref := orm.AcademicReference{ID: "ref-arxiv", SourceDocumentID: "a", RawText: fallbackCitation, ArxivIDBase: "2404.06654", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"target_dataset_id":"papers","reference_ids":["ref-arxiv"],"resolve_external":true,"verify_reference":true,"lookup_fulltext":true}`))
	if w.Code != 200 {
		t.Fatalf("citation preview failed: status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		PreviewToken string        `json:"preview_token"`
		Items        []PreviewItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.PreviewToken == "" || len(response.Items) != 1 || response.Items[0].Title != "A sufficiently long paper title" || len(response.Items[0].ReferenceIDs) != 1 || response.Items[0].ReferenceIDs[0] != ref.ID || len(response.Items[0].Candidates) != 1 {
		t.Fatalf("reference was not verified for ordinary link import: %+v", response)
	}
}

func TestCitationPreviewRejectsReferenceWithoutAccessibleSource(t *testing.T) {
	db := paperFixture(t)
	ref := orm.AcademicReference{ID: "orphan-reference", RawText: fallbackCitation, ArxivIDBase: "2404.06654", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"target_dataset_id":"papers","reference_ids":["orphan-reference"],"verify_reference":true,"lookup_fulltext":true}`))
	if w.Code != http.StatusForbidden {
		t.Fatalf("inaccessible reference was resolved: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCitationPreviewKeepsOpenAlexPDFWhenDOILookupFindsNone(t *testing.T) {
	db := paperFixture(t)
	citationLookupTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host == "scan-control-plane:18080" {
			return 404, `{}`
		}
		if r.URL.Host == "api.openalex.org" && strings.HasPrefix(r.URL.Query().Get("filter"), "title.search:") {
			return 200, `{"results":[{"id":"https://openalex.org/W123","title":"A sufficiently long paper title","publication_year":2024,"doi":"https://doi.org/10.1234/exact","best_oa_location":{"pdf_url":"https://repository.example/paper.pdf","license":"cc-by"}}]}`
		}
		t.Fatalf("unexpected metadata lookup: %s", r.URL)
		return 500, ""
	})
	lookup := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.openalex.org" {
			return 200, `{"results":[]}`
		}
		return 404, ""
	})
	ref := orm.AcademicReference{ID: "ref-openalex-pdf", SourceDocumentID: "a", RawText: fallbackCitation, AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	r := paperRequest(`{"target_dataset_id":"papers","reference_ids":["ref-openalex-pdf"],"resolve_external":true,"verify_reference":true,"lookup_fulltext":true}`)
	r = r.WithContext(context.WithValue(r.Context(), paperPDFClientKey{}, lookup))
	w := httptest.NewRecorder()
	PreviewImport(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Items []PreviewItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || len(response.Items[0].Candidates) != 1 || response.Items[0].Candidates[0].URL != "https://repository.example/paper.pdf" {
		t.Fatalf("verified OpenAlex PDF was discarded: %+v", response.Items)
	}
}

func TestOrdinaryCitationLinkImportPreservesVerifiedReference(t *testing.T) {
	db := paperFixture(t)
	verified := testVerifiedPaper()
	token, err := saveImportPreview(t.Context(), "u", "papers", []PreviewItem{{WorkID: "a", Paper: verified, ReferenceIDs: []string{"ref-a"}, Disposition: "metadata_only"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	CreateImport(w, paperRequest(string(marshal(map[string]any{
		"entry_type": "paper_link", "target_dataset_id": "papers", "preview_token": token,
		"idempotency_key": "ordinary-citation", "items": []map[string]any{{"work_id": "a", "reference_ids": []string{"ref-a"}}},
	}))))
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var item orm.PaperImportItem
	if err := db.Where("academic_work_id=?", "a").Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	var references []string
	if err := json.Unmarshal(item.ReferenceIDsJSON, &references); err != nil || len(references) != 1 || references[0] != "ref-a" {
		t.Fatalf("citation source was lost: %v %v", references, err)
	}
}

func resolveTestCitation(ctx context.Context, db *gorm.DB, ref *orm.AcademicReference) (orm.AcademicWork, WorkInput, error) {
	input, err := verifiedCitationInput(ctx, ref)
	if err != nil {
		return orm.AcademicWork{}, WorkInput{}, err
	}
	work, err := saveVerifiedCitationResolution(ctx, db, ref, input)
	return work, input, err
}
