package academic

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var citationYearTitlePattern = regexp.MustCompile(`(?i)(?:\(|\b)(?:19|20)\d{2}[a-z]?(?:\)|\.)?\s*[.,:]?\s*(.+)$`)
var brokenWordPattern = regexp.MustCompile(`([\p{L}\p{N}])-\s*\p{M}*\s+([\p{L}\p{N}])`)

var arxivRequestGate struct {
	sync.Mutex
	next time.Time
}

func waitForArxiv(ctx context.Context) bool {
	arxivRequestGate.Lock()
	wait := time.Until(arxivRequestGate.next)
	if wait < 0 {
		wait = 0
	}
	arxivRequestGate.next = time.Now().Add(wait + 3*time.Second)
	arxivRequestGate.Unlock()
	if wait == 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

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

type arxivFeed struct {
	Entries []struct {
		ID        string `xml:"id"`
		Title     string `xml:"title"`
		Published string `xml:"published"`
		Authors   []struct {
			Name string `xml:"name"`
		} `xml:"author"`
	} `xml:"entry"`
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
// Failure is intentionally soft because arXiv is a fallback, not a dependency.
func resolveWithArxiv(ctx context.Context, citation string) (WorkInput, bool) {
	query := citationTitle(citation)
	searchField := "ti:"
	if query == "" {
		query = strings.TrimSpace(citation)
		searchField = "all:"
	}
	if len(query) > 300 {
		query = query[:300]
	}
	if strings.TrimSpace(query) == "" {
		return WorkInput{}, false
	}
	u := "https://export.arxiv.org/api/query?search_query=" + searchField + url.QueryEscape(`"`+query+`"`) + "&start=0&max_results=3&sortBy=relevance"
	var feed arxivFeed
	client := &http.Client{Timeout: 10 * time.Second}
	resolved := false
	for attempt := 0; attempt < 2; attempt++ {
		if !waitForArxiv(ctx) {
			return WorkInput{}, false
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", "LazyMind academic resolver/1.0 (opensource@lazyagi.org)")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			decodeErr := xml.NewDecoder(resp.Body).Decode(&feed)
			_ = resp.Body.Close()
			if decodeErr == nil {
				resolved = true
				break
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
	}
	if !resolved {
		return WorkInput{}, false
	}
	bestScore := 0.0
	var best WorkInput
	for _, entry := range feed.Entries {
		title := strings.Join(strings.Fields(entry.Title), " ")
		score := tokenCoverage(citation, title)
		if score < 0.65 || score <= bestScore {
			continue
		}
		base, _ := NormalizeArxivID(entry.ID)
		if base == "" {
			continue
		}
		authors := make([]string, 0, len(entry.Authors))
		for _, author := range entry.Authors {
			if strings.TrimSpace(author.Name) != "" {
				authors = append(authors, strings.TrimSpace(author.Name))
			}
		}
		year := 0
		if len(entry.Published) >= 4 {
			year, _ = strconv.Atoi(entry.Published[:4])
		}
		bestScore = score
		best = WorkInput{Title: title, Authors: authors, Year: year, ArxivID: base, Provider: "arxiv", ProviderWorkID: base, Provenance: map[string]any{"match": "citation_search", "score": score}}
	}
	return best, bestScore > 0
}
