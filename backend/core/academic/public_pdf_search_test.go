package academic

import (
	"lazymind/core/paperlibrary"
	"net/http"
	"strings"
	"testing"
)

func TestAuthorSearchDownloadsMatchedPDFWithoutForwardingSearchKey(t *testing.T) {
	m := paperlibrary.Metadata{Title: "Visual Text Features for Image Matching", Authors: []string{"Sam S. Tsai", "Huizhong Chen", "Bernd Girod"}, Identifiers: paperlibrary.Identifiers{DOI: "10.1109/ISM.2012.84"}}
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.tavily.com" {
			if r.Header.Get("Authorization") != "Bearer test-secret" {
				t.Error("missing search auth")
			}
			return 200, `{"results":[{"title":"Visual Text Features for Image Matching","url":"https://university.example/paper.pdf","content":"Sam S. Tsai, Huizhong Chen, Bernd Girod"}]}`
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("credential sent to PDF host")
		}
		return 206, "%PDF-1.7 test"
	})
	got := lookupAuthorPDFWithClient(t.Context(), client, "test-secret", m)
	if got.Candidate == nil || got.Candidate.URL != "https://university.example/paper.pdf" {
		t.Fatalf("%+v", got)
	}
}
func TestAuthorSearchRejectsUnrelatedTitleAndInsufficientIdentity(t *testing.T) {
	m := paperlibrary.Metadata{Title: "Visual Text Features for Image Matching", Authors: []string{"Sam S. Tsai", "Huizhong Chen"}, Identifiers: paperlibrary.Identifiers{DOI: "10.1109/ISM.2012.84"}}
	for _, result := range []publicPDFSearchResult{
		{Title: "Visual Text Features for Image Matching", URL: "https://repo.example/p.pdf", Content: "different authors"},
		{Title: m.Title, URL: "https://repo.example/p.pdf", Content: "Sam S. Tsai, Huizhong Chen, 10.1109/ISM.2012.840"},
		{Title: "Some other paper", URL: "https://repo.example/p.pdf", Content: "10.1109/ISM.2012.84"},
		{Title: m.Title, URL: "https://127.0.0.1/p.pdf", Content: "10.1109/ISM.2012.84"},
	} {
		if c := authorSearchCandidates([]publicPDFSearchResult{result}, m); len(c) > 0 {
			t.Fatalf("unverified search result accepted: %+v", c)
		}
	}
}
func TestAuthorSearchErrorsDoNotExposeKey(t *testing.T) {
	client := pdfLookupTestClient(func(*http.Request) (int, string) { return 401, "test-secret" })
	got := lookupAuthorPDFWithClient(t.Context(), client, "test-secret", paperlibrary.Metadata{Title: "Some paper title", Identifiers: paperlibrary.Identifiers{DOI: "10.1234/test"}})
	text := strings.Join(got.Warnings, ";")
	if !strings.Contains(text, "401") || strings.Contains(text, "test-secret") {
		t.Fatal(text)
	}
}
