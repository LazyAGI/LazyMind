package academic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxPaperBytes int64 = 64 << 20

func isPublicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}

func validateRemoteURL(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("invalid paper URL")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("paper host cannot be resolved")
	}
	for _, address := range addresses {
		ip := address.IP
		if !isPublicIP(ip) {
			return nil, errors.New("paper URL resolves to a non-public address")
		}
	}
	return u, nil
}

func acquirePDF(ctx context.Context, candidate FulltextCandidate) (path string, size int64, err error) {
	urls := []string{candidate.URL}
	if candidate.SourceProvider == "arxiv" {
		base, _ := NormalizeArxivID(candidate.URL)
		if base != "" {
			urls = []string{
				"https://arxiv.org/pdf/" + base,
				"https://arxiv.org/pdf/" + base + ".pdf",
				"https://export.arxiv.org/pdf/" + base,
			}
		}
	}
	var lastErr error
	for _, rawURL := range urls {
		candidate.URL = rawURL
		path, size, lastErr = acquirePDFOnce(ctx, candidate)
		if lastErr == nil {
			return path, size, nil
		}
	}
	return "", 0, lastErr
}

func acquirePDFOnce(ctx context.Context, candidate FulltextCandidate) (path string, size int64, err error) {
	if _, err = validateRemoteURL(ctx, candidate.URL); err != nil {
		return "", 0, err
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, splitErr
		}
		addresses, lookupErr := net.DefaultResolver.LookupIPAddr(dialCtx, host)
		if lookupErr != nil {
			return nil, lookupErr
		}
		for _, resolved := range addresses {
			if !isPublicIP(resolved.IP) {
				continue
			}
			return dialer.DialContext(dialCtx, network, net.JoinHostPort(resolved.IP.String(), port))
		}
		return nil, errors.New("paper host has no public address")
	}}
	client := &http.Client{Timeout: 2 * time.Minute, Transport: transport}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		_, err := validateRemoteURL(req.Context(), req.URL.String())
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate.URL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "application/pdf")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LazyMind/1.0; +https://github.com/LazyAGI/LazyMind)")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("paper download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxPaperBytes {
		return "", 0, errors.New("paper exceeds size limit")
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "pdf") && !strings.Contains(contentType, "octet-stream") {
		return "", 0, errors.New("paper response is not a PDF")
	}
	tmp, err := os.CreateTemp("", "lazymind-paper-*.pdf")
	if err != nil {
		return "", 0, err
	}
	path = tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxPaperBytes+1))
	if err != nil {
		return "", 0, err
	}
	if written > maxPaperBytes {
		return "", 0, errors.New("paper exceeds size limit")
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	header := make([]byte, 5)
	if _, err = io.ReadFull(tmp, header); err != nil || string(header) != "%PDF-" {
		return "", 0, errors.New("downloaded content is not a valid PDF")
	}
	if written < 1024 {
		return "", 0, errors.New("downloaded PDF is unexpectedly small")
	}
	ok = true
	return path, written, nil
}

func acquireWebDocument(ctx context.Context, rawURL string) (path string, size int64, contentType, extension, sourceURL string, err error) {
	urls := []string{rawURL}
	var lastErr error
	for _, candidateURL := range urls {
		path, size, contentType, extension, lastErr = acquireWebDocumentOnce(ctx, candidateURL)
		if lastErr == nil {
			return path, size, contentType, extension, candidateURL, nil
		}
	}
	return "", 0, "", "", "", lastErr
}

func acquireWebDocumentOnce(ctx context.Context, rawURL string) (path string, size int64, contentType, extension string, err error) {
	if _, err = validateRemoteURL(ctx, rawURL); err != nil {
		return "", 0, "", "", err
	}
	client := &http.Client{Timeout: 45 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		_, redirectErr := validateRemoteURL(req.Context(), req.URL.String())
		return redirectErr
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	req.Header.Set("Accept", "text/html,text/markdown,text/plain;q=0.9")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LazyMind/1.0; +https://github.com/LazyAGI/LazyMind)")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, "", "", fmt.Errorf("reference page returned HTTP %d", resp.StatusCode)
	}
	contentType = strings.ToLower(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	switch {
	case strings.Contains(contentType, "markdown") || strings.HasSuffix(strings.ToLower(resp.Request.URL.Path), ".md"):
		contentType, extension = "text/markdown", ".md"
	case strings.Contains(contentType, "html") || contentType == "":
		contentType, extension = "text/html", ".html"
	case strings.HasPrefix(contentType, "text/plain"):
		contentType, extension = "text/plain", ".txt"
	default:
		return "", 0, "", "", fmt.Errorf("reference page has unsupported content type %s", contentType)
	}
	tmp, err := os.CreateTemp("", "lazymind-reference-*"+extension)
	if err != nil {
		return "", 0, "", "", err
	}
	path = tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, 8<<20+1))
	if err != nil {
		return "", 0, "", "", err
	}
	if written > 8<<20 {
		return "", 0, "", "", errors.New("reference page exceeds size limit")
	}
	if written < 32 {
		return "", 0, "", "", errors.New("reference page is empty")
	}
	ok = true
	return path, written, contentType, extension, nil
}
