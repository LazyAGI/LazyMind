package academic

import (
	"context"
	"strings"
	"testing"
)

func TestCanonicalPaperURLsPreservesArxivRevision(t *testing.T) {
	tests := []struct{ name, input, version, want string }{
		{"PDF URL revision", "https://arxiv.org/pdf/2404.06654v3.pdf?download=1", "", "https://arxiv.org/pdf/2404.06654v3"},
		{"abstract revision", "https://arxiv.org/abs/2404.06654v3", "", "https://arxiv.org/pdf/2404.06654v3"},
		{"candidate fallback revision", "https://arxiv.org/pdf/2404.06654", "v2", "https://arxiv.org/pdf/2404.06654v2"},
		{"matching candidate revision", "arXiv:2404.06654v3", "v3", "https://arxiv.org/pdf/2404.06654v3"},
		{"legacy revision", "https://export.arxiv.org/pdf/math.CA/0611800v2.pdf", "v2", "https://arxiv.org/pdf/math/0611800v2"},
		{"DOI revision", "https://doi.org/10.48550/arXiv.2404.06654v3", "", "https://arxiv.org/pdf/2404.06654v3"},
		{"unversioned legacy candidate", "https://arxiv.org/pdf/2404.06654.pdf", "", "https://arxiv.org/pdf/2404.06654"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls, err := canonicalPaperURLs(FulltextCandidate{URL: tt.input, SourceProvider: "arxiv", Version: tt.version})
			if err != nil {
				t.Fatal(err)
			}
			if len(urls) != 1 || urls[0] != tt.want {
				t.Fatalf("canonical URLs = %#v; want only %s", urls, tt.want)
			}
		})
	}
}

func TestCanonicalPaperURLsRejectsArxivConflictsAndUntrustedURLs(t *testing.T) {
	tests := []struct{ name, input, version string }{
		{"conflicting revisions", "https://arxiv.org/pdf/2404.06654v3", "v2"},
		{"invalid candidate revision", "https://arxiv.org/pdf/2404.06654", "submittedVersion"},
		{"zero candidate revision", "https://arxiv.org/pdf/2404.06654", "v0"},
		{"third party fallback", "https://example.org/2404.06654.pdf", "v3"},
		{"lookalike host", "https://arxiv.org.evil.test/pdf/2404.06654", ""},
		{"userinfo host", "https://arxiv.org@evil.test/pdf/2404.06654", ""},
		{"missing identifier", "https://arxiv.org/pdf/", "v3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls, err := canonicalPaperURLs(FulltextCandidate{URL: tt.input, SourceProvider: " ARXIV ", Version: tt.version})
			if err == nil || len(urls) != 0 {
				t.Fatalf("must reject candidate, got %v %v", urls, err)
			}
			if tt.name == "conflicting revisions" && (!strings.Contains(err.Error(), "v2") || !strings.Contains(err.Error(), "v3")) {
				t.Fatalf("conflict error lost revision details: %v", err)
			}
		})
	}
}

func TestCanonicalPaperURLsRetainsOtherProviderSources(t *testing.T) {
	urls, err := canonicalPaperURLs(FulltextCandidate{URL: "https://publisher.example/paper.pdf", SourceProvider: "openalex", Version: "publishedVersion"})
	if err != nil || len(urls) != 1 || urls[0] != "https://publisher.example/paper.pdf" {
		t.Fatalf("other provider was changed: %#v, %v", urls, err)
	}
}

func TestAcquirePDFRejectsInvalidArxivBeforeNetwork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := acquirePDF(ctx, "user", FulltextCandidate{URL: "https://example.org/2404.06654.pdf", SourceProvider: "arxiv"})
	if err == nil || !strings.Contains(err.Error(), "arXiv") {
		t.Fatalf("must return parse error before attempting network, got %v", err)
	}
}

func TestValidateArxivPDFRedirectRejectsHostAndRevisionChanges(t *testing.T) {
	candidate := FulltextCandidate{URL: "https://arxiv.org/pdf/2404.06654v3", SourceProvider: "arxiv", Version: "v3"}
	for _, target := range []string{"https://arxiv.org/pdf/2404.06654v3.pdf", "https://export.arxiv.org/pdf/2404.06654v3"} {
		if err := validateArxivPDFRedirect(candidate, target); err != nil {
			t.Errorf("same official revision rejected: %v", err)
		}
	}
	for _, target := range []string{"https://example.org/pdf/2404.06654v3", "https://arxiv.org/pdf/2404.06654", "https://arxiv.org/pdf/2404.06654v2", "https://arxiv.org/pdf/2404.06655v3"} {
		if err := validateArxivPDFRedirect(candidate, target); err == nil {
			t.Errorf("unsafe redirect accepted: %s", target)
		}
	}
}

func TestValidateArxivPDFRedirectAllowsLatestResolutionForUnversionedLegacyCandidate(t *testing.T) {
	candidate := FulltextCandidate{URL: "https://arxiv.org/pdf/2404.06654", SourceProvider: "arxiv"}
	if err := validateArxivPDFRedirect(candidate, "https://arxiv.org/pdf/2404.06654v3"); err != nil {
		t.Fatalf("unversioned latest lookup may resolve to a concrete revision: %v", err)
	}
}
