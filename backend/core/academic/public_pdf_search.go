package academic

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"lazymind/core/paperlibrary"
)

type publicPDFSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// Search is a background fallback using the importing user's configured provider.
// Credentials remain server-side and are never attached to candidate downloads.
func lookupAuthorPDF(ctx context.Context, user string, m paperlibrary.Metadata) paperPDFLookup {
	if len(strings.TrimSpace(m.Title)) < 8 || NormalizeDOI(m.Identifiers.DOI) == "" {
		return paperPDFLookup{}
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	results, err := searchPublicPDFs(ctx, user, m.Title)
	if err != nil {
		return paperPDFLookup{Warnings: []string{"作者公开版本搜索: " + err.Error()}, Err: err}
	}
	client, release := paperPDFClient(ctx)
	defer release()
	return probeOpenPDFCandidates(ctx, client, authorSearchCandidates(results, m), m.Identifiers.DOI)
}

// Keep identifier-backed hits as well as unregistered page leads. Search credentials
// are consumed by the existing provider service, never by candidate downloads.
func searchPublicPDFs(ctx context.Context, user, title string) ([]publicPDFSearchResult, error) {
	papers, leads, err := externalPaperSearch(ctx, user, "web", fmt.Sprintf("%q paper PDF", title), 5)
	if err != nil {
		return nil, err
	}
	results := make([]publicPDFSearchResult, 0, len(papers)+len(leads))
	for _, paper := range papers {
		identity := strings.Join(paper.Work.Authors, ", ") + " " + paper.Work.DOI
		results = append(results, publicPDFSearchResult{Title: paper.Work.Title, URL: paper.PaperURL, Content: identity})
		if pdf, ok := paper.Work.Provenance["pdf_url"].(string); ok && pdf != "" && pdf != paper.PaperURL {
			results = append(results, publicPDFSearchResult{Title: paper.Work.Title, URL: pdf, Content: identity})
		}
	}
	for _, lead := range leads {
		results = append(results, publicPDFSearchResult{Title: lead.Title, URL: lead.URL, Content: lead.Snippet})
	}
	return results, nil
}

func authorSearchCandidates(results []publicPDFSearchResult, m paperlibrary.Metadata) []FulltextCandidate {
	out := []FulltextCandidate{}
	doi := NormalizeDOI(m.Identifiers.DOI)
	if doi == "" || len(strings.TrimSpace(m.Title)) < 8 {
		return out
	}
	seen := map[string]bool{}
	for _, r := range results {
		if tokenCoverage(r.Title, m.Title) < 0.95 || tokenCoverage(m.Title, r.Title) < 0.7 {
			continue
		}
		evidence := strings.ToLower(r.Content)
		matches := false
		dois := doiPattern.FindAllString(evidence, -1)
		for _, found := range dois {
			if NormalizeDOI(found) == doi {
				matches = true
			}
		}
		if len(dois) > 0 && !matches {
			continue
		}
		tokens := map[string]bool{}
		for _, token := range strings.Fields(NormalizeTitle(evidence)) {
			tokens[token] = true
		}
		matchedNames := map[string]bool{}
		authors := 0
		for _, name := range m.Authors {
			parts := strings.Fields(name)
			if len(parts) == 0 {
				continue
			}
			last := strings.ToLower(parts[len(parts)-1])
			if len(last) >= 3 && tokens[last] && !matchedNames[last] {
				matchedNames[last] = true
				authors++
			}
		}
		if !matches && authors < 2 {
			continue
		}
		u, err := url.Parse(r.URL)
		if err != nil || !zoteroAllowedURL(u, false) {
			continue
		}
		u.Fragment = ""
		if seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		kind := "open_access_landing"
		if strings.HasSuffix(strings.ToLower(u.Path), ".pdf") {
			kind = "public_pdf"
		}
		out = append(out, FulltextCandidate{URL: u.String(), SourceProvider: "author_search", SourceType: kind, ExpectedMIME: "application/pdf"})
		if len(out) == 4 {
			break
		}
	}
	return out
}
