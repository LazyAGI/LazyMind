package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type openAlexPDFLocation struct {
	IsOA       bool   `json:"is_oa"`
	PDFURL     string `json:"pdf_url"`
	LandingURL string `json:"landing_page_url"`
	License    string `json:"license"`
	Version    string `json:"version"`
}

func fetchOpenAlexPDFCandidates(ctx context.Context, client *http.Client, doi, apiKey string) ([]FulltextCandidate, error) {
	ctx, cancel := context.WithTimeout(ctx, paperPDFMetadataTimeout)
	defer cancel()
	doi = NormalizeDOI(doi)
	value := "https://doi.org/" + doi
	if strings.ContainsAny(value, `,|"`) {
		value = strconv.Quote(value)
	}
	query := url.Values{"filter": {"doi:" + value}, "select": {"doi,best_oa_location,locations"}, "per-page": {"1"}, "mailto": {"opensource@lazyagi.org"}}
	if apiKey != "" {
		query.Set("api_key", apiKey)
		query.Set("select", query.Get("select")+",content_urls")
		authenticated := *client
		authenticated.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &authenticated
	}
	resp, err := fetchPaperSource(ctx, client, "https://api.openalex.org/works?"+query.Encode(), false)
	var requestErr *url.Error
	if apiKey != "" && errors.As(err, &requestErr) {
		err = requestErr.Err
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 2<<20 {
		return nil, pdfLookupError("OpenAlex 元信息响应超过大小限制")
	}
	var record struct {
		Results []struct {
			DOI         string                `json:"doi"`
			Best        *openAlexPDFLocation  `json:"best_oa_location"`
			Locations   []openAlexPDFLocation `json:"locations"`
			ContentURLs struct {
				PDF string `json:"pdf"`
			} `json:"content_urls"`
		} `json:"results"`
	}
	if err = json.Unmarshal(body, &record); err != nil {
		return nil, err
	}
	if record.Results == nil {
		return nil, fmt.Errorf("OpenAlex response is missing results")
	}
	candidates := []FulltextCandidate{}
	seen := map[string]bool{}
	for _, work := range record.Results {
		if NormalizeDOI(work.DOI) != doi {
			return nil, fmt.Errorf("OpenAlex returned a different DOI")
		}
		locations := work.Locations
		if work.Best != nil {
			locations = append([]openAlexPDFLocation{*work.Best}, locations...)
		}
		// Direct PDFs first; landing pages are a second chance, not downloadable files.
		for _, landing := range []bool{false, true} {
			for _, loc := range locations {
				if !loc.IsOA {
					continue
				}
				address, kind := loc.PDFURL, "open_access_pdf"
				if landing {
					address, kind = loc.LandingURL, "open_access_landing"
				}
				u, e := url.Parse(strings.TrimSpace(address))
				if e != nil || !zoteroAllowedURL(u, false) {
					continue
				}
				u.Fragment = ""
				if seen[u.String()] {
					continue
				}
				seen[u.String()] = true
				candidates = append(candidates, FulltextCandidate{URL: u.String(), SourceProvider: "openalex", SourceType: kind, License: loc.License, Version: loc.Version, ExpectedMIME: "application/pdf"})
				if len(candidates) == 4 {
					return candidates, nil
				}
			}
		}
		if u, err := url.Parse(work.ContentURLs.PDF); apiKey != "" && err == nil && u.Scheme == "https" && u.Host == "content.openalex.org" && u.User == nil {
			query := u.Query()
			query.Del("api_key")
			u.RawQuery = query.Encode()
			candidates = append(candidates, FulltextCandidate{URL: u.String(), SourceType: "openalex_hosted", SourceProvider: "openalex", ExpectedMIME: "application/pdf"})
		}
	}
	return candidates, nil
}

// Fetch indexes concurrently, then share one bounded, deduplicated probe queue.
// Failure of either optional provider never prevents another from succeeding.
func lookupIndexedOpenPDF(ctx context.Context, client *http.Client, doi string) paperPDFLookup {
	ctx, cancel := context.WithTimeout(ctx, paperPDFProviderTimeout)
	defer cancel()
	candidates, result := discoverDOIPDFs(ctx, client, doi, "")
	probed := probeOpenPDFCandidates(ctx, client, candidates, doi)
	result.Candidate = probed.Candidate
	result.Warnings = append(result.Warnings, probed.Warnings...)
	return result
}

func discoverDOIPDFs(ctx context.Context, client *http.Client, doi, openAlexKey string) ([]FulltextCandidate, paperPDFLookup) {
	type provider struct {
		name  string
		fetch func(context.Context, *http.Client, string) ([]FulltextCandidate, error)
	}
	providers := []provider{{"Unpaywall", fetchUnpaywallPDFCandidates}, {"OpenAlex", func(ctx context.Context, client *http.Client, doi string) ([]FulltextCandidate, error) {
		return fetchOpenAlexPDFCandidates(ctx, client, doi, openAlexKey)
	}}}
	type outcome struct {
		index      int
		candidates []FulltextCandidate
		err        error
	}
	results := make(chan outcome, len(providers))
	for i, p := range providers {
		go func(i int, p provider) { c, e := p.fetch(ctx, client, doi); results <- outcome{i, c, e} }(i, p)
	}
	outcomes := make([]outcome, len(providers))
	result := paperPDFLookup{}
collect:
	for range providers {
		select {
		case out := <-results:
			outcomes[out.index] = out
		case <-ctx.Done():
			result.Warnings = append(result.Warnings, "开放来源: "+ctx.Err().Error())
			result.Err = ctx.Err()
			result.Retryable = transientPDFError(ctx.Err())
			break collect
		}
	}
	candidates := []FulltextCandidate{}
	seen := map[string]bool{}
	for i, out := range outcomes {
		if out.err != nil {
			result.Warnings = append(result.Warnings, providers[i].name+": "+out.err.Error())
			result.Err = errors.Join(result.Err, out.err)
			result.Retryable = result.Retryable || transientPDFError(out.err)
		}
		for _, c := range out.candidates {
			u, e := url.Parse(c.URL)
			if e != nil {
				continue
			}
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			u.Fragment = ""
			key := u.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			c.URL = key
			candidates = append(candidates, c)
		}
	}
	return candidates, result
}
