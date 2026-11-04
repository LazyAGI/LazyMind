package academic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

const paperPDFLookupTimeout = 6 * time.Second
const paperPDFProviderTimeout = 3 * time.Second
const paperPDFMetadataTimeout = 5 * time.Second

type paperPDFLookup struct {
	Candidate *FulltextCandidate
	Warnings  []string
	Err       error
	Retryable bool
}
type paperPDFClientKey struct{}

func pdfLookupError(detail string) error { return fmt.Errorf("PDF 来源查询失败: %s", detail) }

type paperPDFHTTPError struct {
	status     int
	retryAfter string
}

func (e paperPDFHTTPError) Error() string {
	return fmt.Sprintf("PDF 来源查询失败: HTTP %d", e.status)
}

func transientPDFError(err error) bool {
	var failure paperPDFHTTPError
	if errors.As(err, &failure) {
		status := failure.status
		return status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
	}
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		(errors.As(err, &network) && (network.Timeout() || network.Temporary()))
}

func newPaperPDFClient() *http.Client {
	return &http.Client{Timeout: paperPDFLookupTimeout, Transport: zoteroTransport(false), CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !zoteroAllowedURL(r.URL, false) {
			return pdfLookupError("来源重定向不可用")
		}
		return nil
	}}
}

// All sources share one wall-clock deadline. The caller retains metadata
// when optional PDF lookup fails; manual links never count as PDF candidates.
func lookupDOIPDF(ctx context.Context, doi string) paperPDFLookup {
	client, release := paperPDFClient(ctx)
	defer release()
	return lookupDOIPDFSources(ctx, doi, sciHubSources(), client)
}

func paperPDFClient(ctx context.Context) (*http.Client, func()) {
	if client, _ := ctx.Value(paperPDFClientKey{}).(*http.Client); client != nil {
		return client, func() {}
	}
	client := newPaperPDFClient()
	return client, client.CloseIdleConnections
}

func lookupOpenDOIPDF(ctx context.Context, doi string, publicationOnly ...bool) paperPDFLookup {
	exact := len(publicationOnly) > 0 && publicationOnly[0]
	client, release := paperPDFClient(ctx)
	defer release()
	// Source imports are an explicit lookup. Give discovery and validation
	// separate budgets, bounded below the HTTP caller's 30-second timeout.
	ctx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	var candidates []FulltextCandidate
	seen := map[string]bool{}
	if exact {
		// The registered DOI resolves to the publisher's own landing page. Do
		// not substitute an accepted manuscript or a preprint for this edition.
		address := "https://doi.org/" + url.PathEscape(doi)
		candidates = append(candidates, FulltextCandidate{URL: address, SourceProvider: "publisher", SourceType: "open_access_landing", Version: "publishedVersion", ExpectedMIME: "application/pdf"})
		seen[address] = true
	}
	var discovery, result paperPDFLookup
	for attempt := 0; attempt < 3; attempt++ {
		if attempt == 0 || discovery.Retryable {
			discoveryCtx, stop := context.WithTimeout(ctx, paperPDFMetadataTimeout)
			var found []FulltextCandidate
			found, discovery = discoverDOIPDFs(discoveryCtx, client, doi, "")
			stop()
			for _, candidate := range found {
				if (!exact || candidate.Version == "publishedVersion") && !seen[candidate.URL] {
					seen[candidate.URL] = true
					candidates = append(candidates, candidate)
				}
			}
		}
		probeCtx, stop := context.WithTimeout(ctx, 6*time.Second)
		result = probeOpenPDFCandidates(probeCtx, client, candidates, doi)
		stop()
		result.Warnings = append(append([]string(nil), discovery.Warnings...), result.Warnings...)
		result.Err = errors.Join(discovery.Err, result.Err)
		result.Retryable = discovery.Retryable || result.Retryable
		if result.Candidate != nil || !result.Retryable || attempt == 2 || ctx.Err() != nil {
			return result
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Err = errors.Join(result.Err, ctx.Err())
			return result
		case <-timer.C:
		}
	}
	return result
}

// Keep a small configurable shortlist. A reachable paper page is not
// evidence of a PDF: every candidate must pass the bounded content probe.
var defaultSciHubSources = []string{
	"https://sci-hub.al", "https://sci-hub.mk", "https://sci-hub.ee",
}

func sciHubSources() []string {
	raw := strings.TrimSpace(os.Getenv("ACADEMIC_SCIHUB_BASE_URLS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("ACADEMIC_SCIHUB_BASE_URL"))
	}
	if raw == "" {
		return append([]string(nil), defaultSciHubSources...)
	}
	sources := []string{}
	seen := map[string]bool{}
	for _, base := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base != "" && !seen[base] {
			sources = append(sources, base)
			seen[base] = true
		}
		if len(sources) == 20 {
			break
		}
	}
	return sources
}

func sciHubManualURLs(doi string) []string {
	if strings.TrimSpace(doi) == "" {
		return nil
	}
	urls := []string{"https://doi.org/" + url.PathEscape(doi)}
	for _, base := range sciHubSources() {
		u, err := url.Parse(base)
		if err == nil && zoteroAllowedURL(u, false) && u.RawQuery == "" && u.Fragment == "" {
			u.Path = strings.TrimRight(u.Path, "/") + "/" + doi
			urls = append(urls, u.String())
		}
	}
	return urls
}

func lookupDOIPDFWithClient(ctx context.Context, doi, base string, client *http.Client) paperPDFLookup {
	return lookupDOIPDFSources(ctx, doi, []string{base}, client)
}

func lookupDOIPDFSources(ctx context.Context, doi string, sources []string, client *http.Client) paperPDFLookup {
	ctx, cancel := context.WithTimeout(ctx, paperPDFLookupTimeout)
	defer cancel()
	result := paperPDFLookup{}
	// Share a bounded OA budget across metadata and candidate probes.
	openCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	open := lookupIndexedOpenPDF(openCtx, client, doi)
	stop()
	result.Warnings = append(result.Warnings, open.Warnings...)
	result.Err = open.Err
	if open.Candidate != nil {
		result.Candidate = open.Candidate
		return result
	}
	type outcome struct {
		index     int
		candidate FulltextCandidate
		err       error
	}
	jobs := make(chan int, len(sources))
	results := make(chan outcome, len(sources))
	for i := range sources {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for worker := 0; worker < 3 && worker < len(sources); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				// Page discovery and PDF validation share the remaining lookup
				// deadline; reserve a fair share for any queued source waves.
				deadline, _ := ctx.Deadline()
				waves := (len(sources) - i + 2) / 3
				budget := time.Until(deadline) / time.Duration(waves)
				sourceCtx, done := context.WithTimeout(ctx, budget)
				found, sourceErr := lookupSciHubPDF(sourceCtx, client, sources[i], doi)
				if sourceErr == nil && found.URL != "" {
					found.URL, sourceErr = probePaperPDF(sourceCtx, client, found.URL)
				}
				if sourceCtx.Err() != nil {
					sourceErr = sourceCtx.Err()
				}
				done()
				results <- outcome{i, found, sourceErr}
			}
		}()
	}
	go func() { workers.Wait(); close(results) }()
	failures := make([]string, len(sources))
	for {
		select {
		case <-ctx.Done():
			result.Warnings = append(result.Warnings, "PDF 来源查询已超时或取消")
			for _, warning := range failures {
				if warning != "" {
					result.Warnings = append(result.Warnings, warning)
				}
			}
			return result
		case out, ok := <-results:
			if !ok {
				for _, warning := range failures {
					if warning != "" {
						result.Warnings = append(result.Warnings, warning)
					}
				}
				return result
			}
			if out.err == nil && out.candidate.URL != "" {
				result.Candidate = &out.candidate
				return result // cancel stops remaining in-flight requests; buffered results cannot block workers.
			}
			if out.err != nil {
				failures[out.index] = "Sci-Hub (" + sources[out.index] + "): " + out.err.Error()
			}
		}
	}
}

func fetchPaperSource(ctx context.Context, client *http.Client, address string, probe bool) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || !zoteroAllowedURL(u, false) {
		return nil, pdfLookupError("来源地址不可用")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LazyMind academic resolver/1.0")
	if probe {
		req.Header.Set("Range", "bytes=0-1023")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && !(probe && resp.StatusCode == http.StatusPartialContent) {
		resp.Body.Close()
		return nil, paperPDFHTTPError{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}
	}
	return resp, nil
}

func lookupSciHubPDF(ctx context.Context, client *http.Client, base, doi string) (FulltextCandidate, error) {
	u, err := url.Parse(base)
	if err != nil || !zoteroAllowedURL(u, false) || u.RawQuery != "" || u.Fragment != "" {
		return FulltextCandidate{}, pdfLookupError("Sci-Hub 来源配置无效")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + doi
	resp, err := fetchPaperSource(ctx, client, u.String(), false)
	if err != nil {
		return FulltextCandidate{}, err
	}
	defer resp.Body.Close()
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return FulltextCandidate{}, err
	}
	if bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("%PDF-")) {
		return FulltextCandidate{URL: resp.Request.URL.String(), SourceProvider: "scihub", SourceType: "provider_pdf", ExpectedMIME: "application/pdf"}, nil
	}
	body, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(prefix), resp.Body), (2<<20)+1))
	if err != nil {
		return FulltextCandidate{}, err
	}
	if len(body) > 2<<20 {
		return FulltextCandidate{}, pdfLookupError("来源页面超过大小限制")
	}
	address, err := sciHubPDFURL(body, resp.Request.URL)
	if err != nil {
		return FulltextCandidate{}, err
	}
	return FulltextCandidate{URL: address, SourceProvider: "scihub", SourceType: "provider_pdf", ExpectedMIME: "application/pdf"}, nil
}

func sciHubPDFURL(body []byte, base *url.URL) (string, error) {
	tree, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	var address string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if address != "" {
			return
		}
		if n.Type == html.ElementNode {
			for _, attr := range n.Attr {
				embed := (n.Data == "iframe" || n.Data == "embed") && attr.Key == "src" || n.Data == "object" && attr.Key == "data"
				link := n.Data == "a" && attr.Key == "href"
				if !embed && !link {
					continue
				}
				u, e := url.Parse(strings.TrimSpace(attr.Val))
				if e != nil {
					continue
				}
				u = base.ResolveReference(u)
				u.Fragment = ""
				if link && !strings.HasSuffix(strings.ToLower(u.Path), ".pdf") {
					continue
				}
				if zoteroAllowedURL(u, false) {
					address = u.String()
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(tree)
	if address == "" {
		if bytes.Contains(bytes.ToLower(body), []byte("altcha-widget")) || bytes.Contains(bytes.ToLower(body), []byte("g-recaptcha")) {
			return "", pdfLookupError("Sci-Hub 要求人机验证，未获取 PDF")
		}
		return "", pdfLookupError("Sci-Hub 未返回可用 PDF，可能无记录或需要验证")
	}
	return address, nil
}

func probePaperPDF(ctx context.Context, client *http.Client, address string) (string, error) {
	resp, err := fetchPaperSource(ctx, client, address, true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	if !bytes.Contains(prefix, []byte("%PDF-")) {
		return "", pdfLookupError("来源返回的内容不是 PDF")
	}
	return resp.Request.URL.String(), nil
}
