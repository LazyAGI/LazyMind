package academic

import (
	"context"
	"encoding/json"
	"io"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authorSearchFixture(t *testing.T, status int, items string) {
	t.Helper()
	db := paperFixture(t)
	base := orm.BaseModel{CreateUserID: "u"}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "search", Name: "Tavily", Category: "search", BaseModel: base},
		&orm.UserModelProviderGroup{ID: "search-key", UserModelProviderID: "search", APIKey: "test-secret", IsVerified: true, BaseModel: base},
		&orm.UserSelectedProvider{UserID: "u", Category: "search", UserModelProviderGroupID: "search-key"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ToolConfig map[string]any `json:"tool_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ToolConfig["tavily"] == nil {
			t.Error("missing configured search provider")
		}
		w.WriteHeader(status)
		w.Write([]byte(items))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
}

func TestAuthorSearchDownloadsMatchedPDFWithoutForwardingSearchKey(t *testing.T) {
	authorSearchFixture(t, 200, `{"items":[{"title":"Visual Text Features for Image Matching","url":"https://university.example/paper.pdf","snippet":"Sam S. Tsai, Huizhong Chen, Bernd Girod"}]}`)
	m := paperlibrary.Metadata{Title: "Visual Text Features for Image Matching", Authors: []string{"Sam S. Tsai", "Huizhong Chen", "Bernd Girod"}, Identifiers: paperlibrary.Identifiers{DOI: "10.1109/ISM.2012.84"}}
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential sent to PDF host")
		}
		return 206, "%PDF-1.7 test"
	})
	got := lookupAuthorPDF(context.WithValue(t.Context(), paperPDFClientKey{}, client), "u", m)
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
	authorSearchFixture(t, 401, `{"detail":{"code":"authentication_failed","message":"test-secret"}}`)
	got := lookupAuthorPDF(t.Context(), "u", paperlibrary.Metadata{Title: "Some paper title", Identifiers: paperlibrary.Identifiers{DOI: "10.1234/test"}})
	text := strings.Join(got.Warnings, ";")
	if !strings.Contains(text, "authentication_failed") || strings.Contains(text, "test-secret") {
		t.Fatal(text)
	}
}

func TestPublicPDFSearchKeepsResolvedOfficialPaperLinks(t *testing.T) {
	authorSearchFixture(t, 200, `{"items":[{"title":"Official paper","url":"https://arxiv.org/abs/1902.00751"}]}`)
	previous := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "export.arxiv.org" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(houlsbyAtom("1902.00751", "v1"))), Request: r}, nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	results, err := searchPublicPDFs(t.Context(), "u", houlsbyTitle)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, result := range results {
		if result.URL == "https://arxiv.org/abs/1902.00751" && result.Title == houlsbyTitle {
			found = true
		}
	}
	if !found {
		t.Fatalf("identified official paper lost: %+v", results)
	}
}
