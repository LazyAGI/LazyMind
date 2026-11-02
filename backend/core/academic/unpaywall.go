package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxUnpaywallCandidates = 4

type unpaywallLocation struct {
	URLForPDF string `json:"url_for_pdf"`
	License   string `json:"license"`
	Version   string `json:"version"`
	HostType  string `json:"host_type"`
}
type unpaywallResponse struct {
	BestOALocation *unpaywallLocation  `json:"best_oa_location"`
	OALocations    []unpaywallLocation `json:"oa_locations"`
}

// Keep the recommended location first, then prefer published/accepted copies.
// Embargoed locations and HTML landing pages are not downloadable candidates.
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
		u, err := url.Parse(strings.TrimSpace(l.URLForPDF))
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
		out = append(out, FulltextCandidate{URL: key, SourceProvider: "unpaywall", SourceType: "open_access_pdf", License: l.License, Version: l.Version, ExpectedMIME: "application/pdf"})
		if len(out) == maxUnpaywallCandidates {
			break
		}
	}
	return out
}

func lookupOpenPDF(ctx context.Context, client *http.Client, doi string) paperPDFLookup {
	ctx, stopLookup := context.WithTimeout(ctx, paperPDFProviderTimeout)
	defer stopLookup()
	result := paperPDFLookup{}
	fail := func(err error) paperPDFLookup {
		result.Warnings = append(result.Warnings, "Unpaywall: "+err.Error())
		return result
	}
	metadataCtx, done := context.WithTimeout(ctx, 1500*time.Millisecond)
	req, err := http.NewRequestWithContext(metadataCtx, http.MethodGet, "https://api.unpaywall.org/v2/"+url.PathEscape(NormalizeDOI(doi))+"?email=opensource%40lazyagi.org", nil)
	if err != nil {
		done()
		return fail(err)
	}
	req.Header.Set("User-Agent", "LazyMind academic resolver/1.0")
	resp, err := client.Do(req)
	if err != nil {
		done()
		return fail(err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		done()
		return result
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		done()
		return fail(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	resp.Body.Close()
	done()
	if err != nil {
		return fail(err)
	}
	if len(body) > 2<<20 {
		return fail(pdfLookupError("元信息响应超过大小限制"))
	}
	var record unpaywallResponse
	if err = json.Unmarshal(body, &record); err != nil {
		return fail(err)
	}
	candidates := unpaywallCandidates(record)
	if len(candidates) == 0 {
		return result
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		index     int
		candidate FulltextCandidate
		err       error
	}
	jobs := make(chan int, len(candidates))
	results := make(chan outcome, len(candidates))
	for i := range candidates {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for worker := 0; worker < 2 && worker < len(candidates); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if probeCtx.Err() != nil {
					return
				}
				// Reserve time for queued copies without imposing a 750 ms limit on
				// a lone candidate. Each wave gets a share of the time still left.
				deadline, _ := probeCtx.Deadline()
				waves := (len(candidates) - i + 1) / 2
				budget := time.Until(deadline) / time.Duration(waves)
				attempt, stop := context.WithTimeout(probeCtx, budget)
				candidate := candidates[i]
				address, err := probePaperPDF(attempt, client, candidate.URL)
				candidate.URL = address
				if attempt.Err() != nil {
					err = attempt.Err()
				}
				stop()
				results <- outcome{i, candidate, err}
			}
		}()
	}
	go func() { workers.Wait(); close(results) }()
	failures := make([]string, len(candidates))
	appendFailures := func() {
		for _, failure := range failures {
			if failure != "" {
				result.Warnings = append(result.Warnings, failure)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			appendFailures()
			return fail(ctx.Err())
		case out, ok := <-results:
			if !ok {
				appendFailures()
				return result
			}
			if out.err == nil && out.candidate.URL != "" {
				result.Candidate = &out.candidate
				appendFailures()
				return result
			}
			if out.err != nil {
				failures[out.index] = fmt.Sprintf("Unpaywall 副本 %d: %v", out.index+1, out.err)
			}
		}
	}
}
