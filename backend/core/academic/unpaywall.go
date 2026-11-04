package academic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const maxUnpaywallCandidates = 4

type unpaywallLocation struct {
	LandingURL string `json:"url_for_landing_page"`
	URLForPDF  string `json:"url_for_pdf"`
	License    string `json:"license"`
	Version    string `json:"version"`
	HostType   string `json:"host_type"`
}
type unpaywallResponse struct {
	BestOALocation *unpaywallLocation  `json:"best_oa_location"`
	OALocations    []unpaywallLocation `json:"oa_locations"`
}

// Keep the recommended location first, then prefer published/accepted copies.
// Landing pages are resolved and probed before they can become PDF candidates.
// Embargoed locations are never included.
func unpaywallCandidates(result unpaywallResponse) []FulltextCandidate {
	locations := append([]unpaywallLocation(nil), result.OALocations...)
	rank := func(l unpaywallLocation) int {
		version := map[string]int{"publishedVersion": 0, "acceptedVersion": 2, "submittedVersion": 4}
		n, ok := version[l.Version]
		if !ok {
			n = 6
		}
		if l.HostType != "publisher" {
			n++
		}
		return n
	}
	sort.SliceStable(locations, func(i, j int) bool { return rank(locations[i]) < rank(locations[j]) })
	if result.BestOALocation != nil {
		locations = append([]unpaywallLocation{*result.BestOALocation}, locations...)
	}
	out := []FulltextCandidate{}
	seen := map[string]bool{}
	for _, l := range locations {
		address, sourceType := l.URLForPDF, "open_access_pdf"
		if strings.TrimSpace(address) == "" {
			address, sourceType = l.LandingURL, "open_access_landing"
		}
		u, err := url.Parse(strings.TrimSpace(address))
		if err != nil || !zoteroAllowedURL(u, false) {
			continue
		}
		u.Fragment = ""
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		key := u.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, FulltextCandidate{URL: key, SourceProvider: "unpaywall", SourceType: sourceType, License: l.License, Version: l.Version, ExpectedMIME: "application/pdf"})
		if len(out) == maxUnpaywallCandidates {
			break
		}
	}
	return out
}

func fetchUnpaywallPDFCandidates(ctx context.Context, client *http.Client, doi string) ([]FulltextCandidate, error) {
	metadataCtx, done := context.WithTimeout(ctx, paperPDFMetadataTimeout)
	req, err := http.NewRequestWithContext(metadataCtx, http.MethodGet, "https://api.unpaywall.org/v2/"+url.PathEscape(NormalizeDOI(doi))+"?email=opensource%40lazyagi.org", nil)
	if err != nil {
		done()
		return nil, err
	}
	req.Header.Set("User-Agent", "LazyMind academic resolver/1.0")
	resp, err := client.Do(req)
	if err != nil {
		done()
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		done()
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		done()
		return nil, paperPDFHTTPError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	resp.Body.Close()
	done()
	if err != nil {
		return nil, err
	}
	if len(body) > 2<<20 {
		return nil, pdfLookupError("元信息响应超过大小限制")
	}
	var record unpaywallResponse
	if err = json.Unmarshal(body, &record); err != nil {
		return nil, err
	}
	return unpaywallCandidates(record), nil
}

func lookupOpenPDF(ctx context.Context, client *http.Client, doi string) paperPDFLookup {
	ctx, cancel := context.WithTimeout(ctx, paperPDFProviderTimeout)
	defer cancel()
	candidates, err := fetchUnpaywallPDFCandidates(ctx, client, doi)
	if err != nil {
		return paperPDFLookup{Warnings: []string{"Unpaywall: " + err.Error()}}
	}
	return probeOpenPDFCandidates(ctx, client, candidates, doi)
}
