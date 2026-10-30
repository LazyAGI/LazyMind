package academic

import (
	"context"
	"lazymind/core/academic/arxivsource"
	"regexp"
	"strconv"
	"strings"
)

var citationYearTitlePattern = regexp.MustCompile(`(?i)(?:\(|\b)(?:19|20)\d{2}[a-z]?(?:\)|\.)?\s*[.,:]?\s*(.+)$`)
var brokenWordPattern = regexp.MustCompile(`([\p{L}\p{N}])-\s*\p{M}*\s+([\p{L}\p{N}])`)

// Tests can supply a client for a controlled endpoint without changing the
// official provider's process-wide connection and pacing rules.
var newArxivClient = arxivsource.NewClient

func citationTitle(citation string) string {
	compact := strings.Join(strings.Fields(citation), " ")
	compact = brokenWordPattern.ReplaceAllString(compact, "$1$2")
	compact = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(compact, "")
	match := citationYearTitlePattern.FindStringSubmatch(compact)
	title := ""
	if len(match) >= 2 {
		title = match[1]
		lower := strings.ToLower(strings.TrimSpace(title))
		if strings.HasPrefix(lower, "url ") || strings.HasPrefix(lower, "http") || strings.HasPrefix(lower, "doi:") ||
			strings.Contains(lower, "isbn") || strings.Contains(lower, "association for computing") ||
			strings.HasPrefix(lower, "usenix association") || strings.HasPrefix(lower, "accessed:") {
			title = ""
		}
	}
	markers := []string{". In ", ". arXiv", ". CoRR", ". Proceedings", ". Advances in ", ". URL ", " URL ", " https://", " http://", " doi:"}
	for _, marker := range markers {
		if index := strings.Index(strings.ToLower(title), strings.ToLower(marker)); index > 8 {
			title = title[:index]
		}
	}
	if strings.Trim(strings.TrimSpace(title), ` .,:;"'`) == "" {
		prefix := compact
		for _, marker := range markers {
			if index := strings.Index(strings.ToLower(prefix), strings.ToLower(marker)); index > 8 {
				prefix = prefix[:index]
			}
		}
		prefix = regexp.MustCompile(`(?i)[, .]+(?:19|20)\d{2}[a-z]?[.]?$`).ReplaceAllString(strings.TrimSpace(prefix), "")
		if index := strings.LastIndex(prefix, ". "); index >= 0 {
			title = prefix[index+2:]
		}
	}
	return strings.Trim(strings.TrimSpace(title), ` .,:;"'`)
}

func tokenCoverage(reference, title string) float64 {
	referenceTokens := map[string]bool{}
	for _, token := range strings.Fields(NormalizeTitle(reference)) {
		referenceTokens[token] = true
	}
	titleTokens := strings.Fields(NormalizeTitle(title))
	if len(titleTokens) == 0 {
		return 0
	}
	matched := 0
	for _, token := range titleTokens {
		if referenceTokens[token] {
			matched++
		}
	}
	return float64(matched) / float64(len(titleTokens))
}

// resolveWithArxiv provides the deterministic domain adapter used by imports.
// Provider failures are returned separately from a successful search with no match.
func resolveWithArxiv(ctx context.Context, citation string) (WorkInput, bool, error) {
	query := citationTitle(citation)
	field := "ti:"
	if query == "" {
		query, field = strings.TrimSpace(citation), "all:"
	}
	if len(query) > 300 {
		query = query[:300]
	}
	papers, err := newArxivClient().Search(ctx, field+strconv.Quote(query), 5)
	if err != nil {
		return WorkInput{}, false, err
	}
	bestScore := 0.0
	var best WorkInput
	for _, paper := range papers {
		score := tokenCoverage(citation, paper.Title)
		if score < 0.65 || score <= bestScore {
			continue
		}
		bestScore, best = score, arxivWorkInput(paper)
	}
	return best, bestScore > 0, nil
}

func arxivWorkInput(paper arxivsource.Metadata) WorkInput {
	return WorkInput{Title: paper.Title, Authors: paper.Authors, Abstract: paper.Abstract,
		Year: paper.Year, Venue: paper.Venue, DOI: paper.DOI, ArxivID: paper.BaseID,
		Provider: "arxiv", ProviderWorkID: paper.BaseID,
		Provenance: map[string]any{"pdf_url": paper.PDFURL, "pdf_version": paper.Version, "source": "arxiv"}}
}
