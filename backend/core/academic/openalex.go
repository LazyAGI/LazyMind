package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func resolveDOIOpenAccess(ctx context.Context, doi string) (string, string, string, error) {
	if NormalizeDOI(doi) == "" {
		return "", "", "", nil
	}
	u := "https://api.unpaywall.org/v2/" + url.PathEscape(NormalizeDOI(doi)) + "?email=opensource%40lazyagi.org"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("Unpaywall request failed: %w", err)
	}
	req.Header.Set("User-Agent", "LazyMind academic resolver/1.0")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("Unpaywall lookup failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", "", "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("Unpaywall lookup returned HTTP %d", resp.StatusCode)
	}
	var result unpaywallResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", "", fmt.Errorf("Unpaywall response is invalid: %w", err)
	}
	if result.BestOALocation == nil {
		return "", "", "", nil
	}
	return strings.TrimSpace(result.BestOALocation.URLForPDF), result.BestOALocation.License, result.BestOALocation.Version, nil
}

// resolveWithOpenAlex resolves metadata from an independent paper index.
// Strict title/year checks prevent unrelated same-name results; provider
// failures propagate separately from a successful search with no match.
func resolveWithOpenAlex(ctx context.Context, citation string) (WorkInput, bool, error) {
	title := citationTitle(citation)
	if len(title) < 8 {
		return WorkInput{}, false, nil
	}
	u := "https://api.openalex.org/works?filter=title.search:" + url.QueryEscape(`"`+title+`"`) +
		"&per-page=10&select=id,title,publication_year,doi,ids,best_oa_location&mailto=opensource%40lazyagi.org"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return WorkInput{}, false, fmt.Errorf("OpenAlex request failed: %w", err)
	}
	req.Header.Set("User-Agent", "LazyMind academic resolver/1.0 (https://github.com/LazyAGI/LazyMind)")
	var result openAlexResponse
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return WorkInput{}, false, fmt.Errorf("OpenAlex lookup failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WorkInput{}, false, fmt.Errorf("OpenAlex lookup returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return WorkInput{}, false, fmt.Errorf("OpenAlex response is invalid: %w", err)
	}
	if result.Results == nil {
		return WorkInput{}, false, fmt.Errorf("OpenAlex response is missing results")
	}
	year := extractYear(citation)
	bestScore := 0.0
	var best WorkInput
	var providerErrors []error
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
		} else {
			pdfURL, license, version, err := resolveDOIOpenAccess(ctx, doi)
			if err != nil {
				providerErrors = append(providerErrors, err)
			}
			if pdfURL != "" {
				provenance["pdf_url"] = pdfURL
				provenance["pdf_license"] = license
				provenance["pdf_version"] = version
			}
		}
		bestScore = rank
		best = WorkInput{TrustedFulltext: true, Title: entry.Title, Year: entry.PublicationYear, DOI: doi, ArxivID: arxivID,
			Provider: "openalex", ProviderWorkID: strings.TrimPrefix(entry.ID, "https://openalex.org/"), Provenance: provenance}
	}
	return best, bestScore > 0, errors.Join(providerErrors...)
}
