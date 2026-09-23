package academic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type openAlexResponse struct {
	Results []struct {
		ID              string `json:"id"`
		Title           string `json:"title"`
		PublicationYear int    `json:"publication_year"`
		DOI             string `json:"doi"`
		IDs             struct {
			Arxiv string `json:"arxiv"`
		} `json:"ids"`
		BestOALocation *struct {
			PDFURL  string `json:"pdf_url"`
			License string `json:"license"`
			Version string `json:"version"`
		} `json:"best_oa_location"`
	} `json:"results"`
}

type unpaywallResponse struct {
	BestOALocation *struct {
		URLForPDF string `json:"url_for_pdf"`
		License   string `json:"license"`
		Version   string `json:"version"`
	} `json:"best_oa_location"`
}

func resolveDOIOpenAccess(ctx context.Context, doi string) (string, string, string) {
	if NormalizeDOI(doi) == "" {
		return "", "", ""
	}
	u := "https://api.unpaywall.org/v2/" + url.PathEscape(NormalizeDOI(doi)) + "?email=opensource%40lazyagi.org"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "LazyMind academic resolver/1.0")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", ""
	}
	var result unpaywallResponse
	if json.NewDecoder(resp.Body).Decode(&result) != nil || result.BestOALocation == nil {
		return "", "", ""
	}
	return strings.TrimSpace(result.BestOALocation.URLForPDF), result.BestOALocation.License, result.BestOALocation.Version
}

// resolveWithOpenAlex is the secondary deterministic resolver. It is used
// when arXiv search is rate-limited or a paper is hosted by another open
// repository. Strict title/year checks prevent unrelated same-name results.
func resolveWithOpenAlex(ctx context.Context, citation string) (WorkInput, bool) {
	title := citationTitle(citation)
	if len(title) < 8 {
		return WorkInput{}, false
	}
	u := "https://api.openalex.org/works?filter=title.search:" + url.QueryEscape(`"`+title+`"`) +
		"&per-page=10&select=id,title,publication_year,doi,ids,best_oa_location&mailto=opensource%40lazyagi.org"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "LazyMind academic resolver/1.0 (https://github.com/LazyAGI/LazyMind)")
	var result openAlexResponse
	client := &http.Client{Timeout: 8 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := client.Do(req.Clone(ctx))
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			decodeErr := json.NewDecoder(resp.Body).Decode(&result)
			_ = resp.Body.Close()
			if decodeErr == nil {
				break
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if attempt == 2 {
			return WorkInput{}, false
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return WorkInput{}, false
		case <-timer.C:
		}
	}
	year := extractYear(citation)
	bestScore := 0.0
	var best WorkInput
	for _, entry := range result.Results {
		score := tokenCoverage(title, entry.Title)
		arxivID, _ := NormalizeArxivID(entry.IDs.Arxiv)
		doi := NormalizeDOI(entry.DOI)
		if arxivID == "" && strings.HasPrefix(doi, "10.48550/arxiv.") {
			arxivID, _ = NormalizeArxivID(strings.TrimPrefix(doi, "10.48550/arxiv."))
		}
		hasPDF := entry.BestOALocation != nil && strings.TrimSpace(entry.BestOALocation.PDFURL) != ""
		rank := score
		if arxivID != "" || hasPDF {
			rank += 0.1
		}
		if score < 0.8 || rank <= bestScore {
			continue
		}
		if year > 0 && entry.PublicationYear > 0 && (entry.PublicationYear < year-1 || entry.PublicationYear > year+1) {
			continue
		}
		provenance := map[string]any{"match": "openalex_title", "score": score, "openalex_id": entry.ID}
		if entry.BestOALocation != nil && strings.TrimSpace(entry.BestOALocation.PDFURL) != "" {
			provenance["pdf_url"] = entry.BestOALocation.PDFURL
			provenance["pdf_license"] = entry.BestOALocation.License
			provenance["pdf_version"] = entry.BestOALocation.Version
		} else if pdfURL, license, version := resolveDOIOpenAccess(ctx, doi); pdfURL != "" {
			provenance["pdf_url"] = pdfURL
			provenance["pdf_license"] = license
			provenance["pdf_version"] = version
		}
		bestScore = rank
		best = WorkInput{Title: entry.Title, Year: entry.PublicationYear, DOI: doi, ArxivID: arxivID,
			Provider: "openalex", ProviderWorkID: strings.TrimPrefix(entry.ID, "https://openalex.org/"), Provenance: provenance}
	}
	return best, bestScore > 0
}
