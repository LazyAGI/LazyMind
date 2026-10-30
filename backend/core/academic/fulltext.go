package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"net/url"
	"os"
	"strings"
	"time"
)

type contentAttempt struct {
	URL       string `json:"url"`
	Status    string `json:"status"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Error     string `json:"error,omitempty"`
}
type contentAcquisition struct {
	Candidate      *FulltextCandidate `json:"candidate,omitempty"`
	StartedAt      time.Time          `json:"started_at"`
	SourceURL      string             `json:"source_url,omitempty"`
	SourceProvider string             `json:"source_provider,omitempty"`
	Downloaded     bool               `json:"downloaded"`
	PDFHash        string             `json:"pdf_hash,omitempty"`
	Attempts       []contentAttempt   `json:"attempts"`
	FileRef        string             `json:"file_ref,omitempty"`
	ExpiresAt      time.Time          `json:"expires_at,omitempty"`
}

func paperContentAcquisition(data json.RawMessage) contentAcquisition {
	var quality contentQuality
	_ = json.Unmarshal(data, &quality)
	if !quality.Acquisition.StartedAt.IsZero() {
		return quality.Acquisition
	}
	var acquisition contentAcquisition
	_ = json.Unmarshal(data, &acquisition)
	return acquisition
}

func saveContentAcquisition(ctx context.Context, content orm.AcademicPaperContent, acquisition contentAcquisition) error {
	quality := contentQuality{Acquisition: acquisition, Downloaded: acquisition.Downloaded, Identity: "unconfirmed", Material: "unknown", Text: "suspect"}
	return store.DB().WithContext(ctx).Model(&content).Update("quality_json", marshal(quality)).Error
}

func doiFulltextCandidates(ctx context.Context, user, doi string) ([]FulltextCandidate, error) {
	key, err := modelconfig.LoadOpenAlexAPIKey(ctx, store.DB(), user)
	if err != nil {
		return nil, err
	}
	var work scholarlyWork
	err = openAlexGet(ctx, "works/"+url.PathEscape("https://doi.org/"+NormalizeDOI(doi)), url.Values{}, key, &work)
	if errors.Is(err, errOpenAlexNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return scholarlyPaper(work).Candidates, nil
}

// Page acquisition uses OriginalService's DNS, IP, redirect and byte validation.
// Only explicit PDF links are followed; every resulting file is checked again.
func publicPagePDFs(ctx context.Context, user, address string) ([]string, error) {
	svc, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: store.DB()})
	if err != nil {
		return nil, err
	}
	caller := doc.DatasetCatalogCaller{UserID: user}
	ref, err := svc.AcquireRemoteOriginal(ctx, caller, doc.AcquireRemoteOriginalRequest{URL: address, MaxBytes: 2 << 20})
	if err != nil {
		return nil, err
	}
	defer svc.ReleaseOriginal(context.WithoutCancel(ctx), caller, ref.FileRef)
	file, err := svc.OpenOriginal(ctx, caller, ref.FileRef)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 2<<20))
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(string(data), "%PDF-") {
		return []string{address}, nil
	}
	document, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(ref.FinalURL)
	if err != nil {
		return nil, err
	}
	links := []string{}
	seen := map[string]bool{}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "meta" || node.Data == "a") {
			attrs := map[string]string{}
			for _, a := range node.Attr {
				attrs[strings.ToLower(a.Key)] = a.Val
			}
			candidate := ""
			if node.Data == "meta" && strings.EqualFold(attrs["name"], "citation_pdf_url") {
				candidate = attrs["content"]
			}
			if node.Data == "a" && strings.Contains(strings.ToLower(attrs["href"]), ".pdf") {
				candidate = attrs["href"]
			}
			if candidate != "" {
				if u, e := url.Parse(candidate); e == nil {
					resolved := base.ResolveReference(u)
					if (resolved.Scheme == "http" || resolved.Scheme == "https") && resolved.User == nil && !seen[resolved.String()] && len(links) < 5 {
						seen[resolved.String()] = true
						links = append(links, resolved.String())
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return links, nil
}
func readExternalCopies(ctx context.Context, user string, source paperSource, content orm.AcademicPaperContent, originals *doc.OriginalService) ([]ContentPage, string, string, string, error) {
	acquisition := paperContentAcquisition(content.QualityJSON)
	acquisition.StartedAt = time.Now().UTC()
	runCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	candidates := source.fulltextCandidates()
	if acquisition.FileRef != "" && acquisition.Candidate != nil {
		candidates = append([]FulltextCandidate{*acquisition.Candidate}, candidates...)
	}
	if source.Paper.PaperURL != "" {
		candidates = append(candidates, FulltextCandidate{URL: source.Paper.PaperURL, SourceProvider: "web", SourceType: "reference_page"})
	}
	save := func() error { return saveContentAcquisition(ctx, content, acquisition) }
	if err := save(); err != nil {
		return nil, "", "", "", err
	}
	caller := doc.DatasetCatalogCaller{UserID: user}
	seen := map[string]bool{}
	doiSearched, webSearched := source.Paper.DOI == "", false
	var failures []error
	var rejected []ContentPage
	var rejectedHash, rejectedVersion string
	attempts := 0
	for index := 0; index < len(candidates) || !doiSearched || !webSearched; index++ {
		if runCtx.Err() != nil {
			return nil, "", "", "", errors.New("单篇取文预算已用尽")
		}
		if index >= len(candidates) && !doiSearched {
			doiSearched = true
			started := time.Now()
			copies, err := doiFulltextCandidates(runCtx, user, source.Paper.DOI)
			attempt := contentAttempt{URL: "https://doi.org/" + source.Paper.DOI, Status: "doi_lookup", ElapsedMS: time.Since(started).Milliseconds()}
			if err != nil {
				attempt.Status, attempt.Error = "lookup_failed", err.Error()
				failures = append(failures, err)
			}
			candidates = append(candidates, copies...)
			acquisition.Attempts = append(acquisition.Attempts, attempt)
			if err = save(); err != nil {
				return nil, "", "", "", err
			}
		}
		if index >= len(candidates) && !webSearched {
			webSearched = true
			started := time.Now()
			_, leads, err := externalPaperSearch(runCtx, user, "web", "\""+source.Paper.Title+"\" paper PDF", 5)
			attempt := contentAttempt{Status: "web_search", ElapsedMS: time.Since(started).Milliseconds()}
			if err != nil {
				attempt.Status, attempt.Error = "search_failed", err.Error()
				failures = append(failures, err)
			} else {
				for _, lead := range leads {
					kind := "reference_page"
					if u, err := url.Parse(lead.URL); err == nil && strings.HasSuffix(strings.ToLower(u.Path), ".pdf") {
						kind = "open_access_pdf"
					}
					candidates = append(candidates, FulltextCandidate{URL: lead.URL, SourceProvider: "web", SourceType: kind})
				}
			}
			acquisition.Attempts = append(acquisition.Attempts, attempt)
			if err = save(); err != nil {
				return nil, "", "", "", err
			}
		}
		if index >= len(candidates) {
			break
		}
		candidate := candidates[index]
		// Landing pages are discovery leads, not verified download candidates.
		if candidate.SourceType != "reference_page" {
			var ok bool
			candidate, ok = source.matchingCandidate(candidate)
			if !ok {
				continue
			}
		}
		if candidate.URL == "" || seen[candidate.SourceType+":"+candidate.URL] {
			continue
		}
		seen[candidate.SourceType+":"+candidate.URL] = true
		started := time.Now()
		if candidate.SourceType == "reference_page" {
			links, err := publicPagePDFs(runCtx, user, candidate.URL)
			attempt := contentAttempt{URL: candidate.URL, Status: "page_read", ElapsedMS: time.Since(started).Milliseconds()}
			if err != nil {
				attempt.Status, attempt.Error = "page_failed", err.Error()
				failures = append(failures, err)
			} else {
				for _, link := range links {
					candidates = append(candidates, FulltextCandidate{URL: link, SourceProvider: "web", SourceType: "open_access_pdf"})
				}
			}
			acquisition.Attempts = append(acquisition.Attempts, attempt)
			if err = save(); err != nil {
				return nil, "", "", "", err
			}
			continue
		}
		file, err := acquireContentOriginal(runCtx, originals, caller, candidate, &acquisition)
		attempt := contentAttempt{URL: candidate.URL, Status: "download_failed"}
		if err == nil {
			if err = save(); err != nil {
				_ = file.Close()
				return nil, "", "", "", err
			}
			pages, hash, parseErr := parsePaperPDF(runCtx, file.Name(), acquisition.PDFHash)
			_ = file.Close()
			if parseErr == nil {
				rejected, rejectedHash, rejectedVersion = pages, hash, candidate.Version
				q := checkPaperQuality(source.Paper, pages, "external", hash)
				attempt.Status = "needs_review"
				if q.Eligible {
					attempt.Status, attempt.ElapsedMS = "ready", time.Since(started).Milliseconds()
					acquisition.Attempts = append(acquisition.Attempts, attempt)
					if err = save(); err != nil {
						return nil, "", "", "", err
					}
					return pages, "external", hash, candidate.Version, nil
				}
				attempt.Error = q.Reason
			} else {
				attempt.Status, attempt.Error = "parse_failed", parseErr.Error()
				failures = append(failures, fmt.Errorf("PDF 已获取，但解析失败：%w", parseErr))
			}
		} else {
			attempt.Error = err.Error()
			failures = append(failures, fmt.Errorf("PDF 下载失败：%w", err))
		}
		attempt.ElapsedMS = time.Since(started).Milliseconds()
		acquisition.Attempts = append(acquisition.Attempts, attempt)
		if err = save(); err != nil {
			return nil, "", "", "", err
		}
		attempts++
		if attempts >= 12 {
			break
		}
	}
	if len(rejected) > 0 {
		return rejected, "external", rejectedHash, rejectedVersion, nil
	}
	if len(failures) > 0 {
		return nil, "external", acquisition.PDFHash, "", errors.Join(failures...)
	}
	return nil, "", "", "", errFulltextUnavailable
}

// Keep external PDFs in OriginalService's existing temporary store.
// Parsing retries reuse these immutable bytes; the service owns expiry and cleanup.
func acquireContentOriginal(ctx context.Context, service *doc.OriginalService, caller doc.DatasetCatalogCaller, candidate FulltextCandidate, acquisition *contentAcquisition) (*os.File, error) {
	if acquisition.FileRef != "" && acquisition.Candidate != nil && acquisition.Candidate.URL == candidate.URL && time.Now().Before(acquisition.ExpiresAt) {
		file, err := service.OpenOriginal(ctx, caller, acquisition.FileRef)
		if err == nil {
			return file, nil
		}
		var serviceError *doc.DatasetServiceError
		if !os.IsNotExist(err) && !(errors.As(err, &serviceError) && (serviceError.Code == doc.DatasetServiceNotFound || serviceError.Code == "SOURCE_EXPIRED")) {
			return nil, err
		}
	}
	original, err := acquireCandidateOriginal(ctx, service, caller, candidate)
	if err != nil {
		return nil, err
	}
	acquisition.FileRef, acquisition.ExpiresAt = original.FileRef, original.ExpiresAt
	acquisition.Downloaded, acquisition.PDFHash = true, original.ContentSHA256
	acquisition.SourceURL, acquisition.SourceProvider = candidate.URL, candidate.SourceProvider
	acquisition.Candidate = &candidate
	return service.OpenOriginal(ctx, caller, original.FileRef)
}
