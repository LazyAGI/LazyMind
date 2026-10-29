package academic

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/net/html"
	"io"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
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
}
type contentAcquisition struct {
	Candidate      *FulltextCandidate `json:"candidate,omitempty"`
	StartedAt      time.Time          `json:"started_at"`
	SourceURL      string             `json:"source_url,omitempty"`
	SourceProvider string             `json:"source_provider,omitempty"`
	Downloaded     bool               `json:"downloaded"`
	PDFHash        string             `json:"pdf_hash,omitempty"`
	Attempts       []contentAttempt   `json:"attempts"`
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
func readExternalCopies(ctx context.Context, user string, source paperSource, content orm.AcademicPaperContent) ([]ContentPage, string, string, string, error) {
	acquisition := contentAcquisition{}
	var previous contentQuality
	_ = json.Unmarshal(content.QualityJSON, &previous)
	acquisition = previous.Acquisition
	if acquisition.StartedAt.IsZero() {
		_ = json.Unmarshal(content.QualityJSON, &acquisition)
	}
	if acquisition.StartedAt.IsZero() {
		acquisition.StartedAt = time.Now().UTC()
	}
	runCtx, cancel := context.WithDeadline(ctx, acquisition.StartedAt.Add(4*time.Minute))
	defer cancel()
	candidates := append([]FulltextCandidate{}, source.Candidates...)
	hasPrimary := false
	for _, candidate := range candidates {
		if candidate.URL == source.Paper.PDFURL {
			hasPrimary = true
		}
	}
	if source.Paper.PDFURL != "" && !hasPrimary {
		candidates = append([]FulltextCandidate{{URL: source.Paper.PDFURL, SourceProvider: source.Paper.SourceProvider, Version: source.Paper.SourceVersion, SourceType: "open_access_pdf"}}, candidates...)
	}
	if len(candidates) == 0 && source.Paper.DOI != "" {
		address, license, version, err := resolveDOIOpenAccess(runCtx, source.Paper.DOI)
		if err == nil && address != "" {
			candidates = append(candidates, FulltextCandidate{URL: address, Version: version, License: license, SourceProvider: "unpaywall", SourceType: "open_access_pdf"})
		}
	}
	if source.Paper.PaperURL != "" {
		candidates = append(candidates, FulltextCandidate{URL: source.Paper.PaperURL, SourceProvider: "web", SourceType: "reference_page"})
	}
	save := func() error {
		return store.DB().WithContext(ctx).Model(&content).Update("quality_json", marshal(acquisition)).Error
	}
	if err := save(); err != nil {
		return nil, "", "", "", err
	}
	seen := map[string]bool{}
	webSearched := false
	var rejected []ContentPage
	var rejectedHash, rejectedVersion string
	for index := 0; index < len(candidates) || !webSearched; index++ {
		if runCtx.Err() != nil {
			return nil, "", "", "", errors.New("单篇取文预算已用尽")
		}
		if index >= len(candidates) {
			webSearched = true
			_, leads, e := externalPaperSearch(runCtx, user, "web", `"`+source.Paper.Title+`" paper PDF`, 5)
			if e == nil {
				for _, lead := range leads {
					kind := "reference_page"
					if u, err := url.Parse(lead.URL); err == nil && strings.HasSuffix(strings.ToLower(u.Path), ".pdf") {
						kind = "open_access_pdf"
					}
					candidates = append(candidates, FulltextCandidate{URL: lead.URL, SourceProvider: "web", SourceType: kind})
				}
			}
			if index >= len(candidates) {
				break
			}
		}
		candidate := candidates[index]
		if candidate.URL == "" || seen[candidate.SourceType+":"+candidate.URL] {
			continue
		}
		seen[candidate.SourceType+":"+candidate.URL] = true
		if source.Paper.SourceProvider == "arxiv" && source.Paper.SourceVersion != "" {
			candidate.Version = source.Paper.SourceVersion
			if officialArxivID(candidate.URL) != source.Paper.ArxivID {
				continue
			}
		}
		if source.Paper.SourceVersion != "" && candidate.Version != "" && candidate.Version != source.Paper.SourceVersion {
			continue
		}
		started := time.Now()
		if candidate.SourceType == "reference_page" {
			links, e := publicPagePDFs(runCtx, user, candidate.URL)
			status := "no_pdf_link"
			if e == nil {
				status = "page_read"
				for _, link := range links {
					candidates = append(candidates, FulltextCandidate{URL: link, SourceProvider: "web", SourceType: "open_access_pdf", Version: candidate.Version})
				}
			}
			acquisition.Attempts = append(acquisition.Attempts, contentAttempt{candidate.URL, status, time.Since(started).Milliseconds()})
			if e = save(); e != nil {
				return nil, "", "", "", e
			}
			continue
		}
		path, _, err := acquirePDF(runCtx, user, candidate)
		status := "download_failed"
		if err == nil {
			pages, hash, e := parsePaperPDF(runCtx, path, "")
			acquisition.Downloaded = true
			acquisition.PDFHash = hash
			acquisition.SourceURL = candidate.URL
			acquisition.SourceProvider = candidate.SourceProvider
			_ = os.Remove(path)
			if e == nil {
				rejected, rejectedHash, rejectedVersion = pages, hash, candidate.Version
				q := checkPaperQuality(source.Paper, pages, "external", hash)
				status = "needs_review"
				if q.Eligible {
					acquisition.Candidate = &candidate
					acquisition.SourceURL = candidate.URL
					acquisition.Attempts = append(acquisition.Attempts, contentAttempt{candidate.URL, "ready", time.Since(started).Milliseconds()})
					if e = save(); e != nil {
						return nil, "", "", "", e
					}
					return pages, "external", hash, candidate.Version, nil
				}
			} else {
				status = "parse_failed"
			}
		}
		acquisition.Attempts = append(acquisition.Attempts, contentAttempt{candidate.URL, status, time.Since(started).Milliseconds()})
		if err = save(); err != nil {
			return nil, "", "", "", err
		}
		if len(acquisition.Attempts) >= 12 {
			break
		}
	}
	if len(rejected) > 0 {
		return rejected, "external", rejectedHash, rejectedVersion, nil
	}
	return nil, "", "", "", errFulltextUnavailable
}
