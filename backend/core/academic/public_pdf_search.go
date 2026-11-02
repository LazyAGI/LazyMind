package academic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lazymind/core/modelconfig"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

type publicPDFSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// Search is a background fallback using the importing user's configured provider.
// Credentials remain server-side and are never attached to candidate downloads.
func lookupAuthorPDF(ctx context.Context, user string, m paperlibrary.Metadata) paperPDFLookup {
	config, err := modelconfig.LoadSearchToolConfig(ctx, store.DB(), user)
	if err != nil {
		return paperPDFLookup{Warnings: []string{"作者公开版本检索配置不可用"}}
	}
	key, _ := config["tavily"].(string)
	if keys, ok := config["tavily"].([]string); ok && len(keys) > 0 {
		key = keys[0]
	}
	if key == "" {
		return paperPDFLookup{Warnings: []string{"未配置可用的 Tavily 搜索服务，未执行作者公开版本检索"}}
	}
	client := newPaperPDFClient()
	defer client.CloseIdleConnections()
	return lookupAuthorPDFWithClient(ctx, client, key, m)
}

func lookupAuthorPDFWithClient(ctx context.Context, client *http.Client, key string, m paperlibrary.Metadata) paperPDFLookup {
	result := paperPDFLookup{}
	if len(strings.TrimSpace(m.Title)) < 8 || NormalizeDOI(m.Identifiers.DOI) == "" {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"query": fmt.Sprintf("%q PDF", m.Title), "max_results": 5, "search_depth": "basic", "include_answer": false, "include_raw_content": false})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(payload))
	if err != nil {
		return result
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	// An authenticated API request must not follow redirects to result hosts.
	searchClient := *client
	searchClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := searchClient.Do(request)
	if err != nil {
		return paperPDFLookup{Warnings: []string{"作者公开版本搜索请求失败或超时"}}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return paperPDFLookup{Warnings: []string{fmt.Sprintf("作者公开版本搜索 HTTP %d", response.StatusCode)}}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return paperPDFLookup{Warnings: []string{"作者公开版本搜索响应读取失败或超过大小限制"}}
	}
	var matches struct {
		Results []publicPDFSearchResult `json:"results"`
	}
	if json.Unmarshal(body, &matches) != nil || matches.Results == nil {
		return paperPDFLookup{Warnings: []string{"作者公开版本搜索响应无效"}}
	}
	return probeOpenPDFCandidates(ctx, client, authorSearchCandidates(matches.Results, m), m.Identifiers.DOI)
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
