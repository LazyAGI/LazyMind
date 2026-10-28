package doc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

func publicOriginalIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	// Go's private predicate excludes shared, benchmarking, reserved and documentation ranges.
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(cidr).Contains(addr) {
			return false
		}
	}
	return true
}
func validateOriginalURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("original URL must be HTTP(S) without credentials")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicOriginalIP(ip) {
		return nil, errors.New("original URL uses a non-public address")
	}
	return u, nil
}
func newOriginalHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{DisableCompression: true, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("original host has no addresses")
		}
		for _, ip := range ips {
			if !publicOriginalIP(ip.IP) {
				return nil, errors.New("original host resolves to a non-public address")
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}}
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many original redirects")
		}
		_, err := validateOriginalURL(req.URL.String())
		return err
	}}
}
func (s *OriginalService) AcquireRemoteOriginal(ctx context.Context, caller DatasetCatalogCaller, req AcquireRemoteOriginalRequest) (OriginalRef, error) {
	if err := validOriginalCaller(caller); err != nil {
		return OriginalRef{}, err
	}
	u, err := validateOriginalURL(req.URL)
	if err != nil {
		return OriginalRef{}, err
	}
	client := newOriginalHTTPClient()
	defer client.CloseIdleConnections()
	if s.httpTransport != nil {
		client.Transport = s.httpTransport
	}
	downloadURL := *u
	params := downloadURL.Query()
	for key, value := range req.PrivateQuery {
		params.Set(key, value)
	}
	downloadURL.RawQuery = params.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), nil)
	if err != nil {
		return OriginalRef{}, err
	}
	request.Header.Set("User-Agent", "LazyMind/1.0")
	request.Header.Set("Accept-Encoding", "identity")
	if req.ExpectedMIME != "" {
		request.Header.Set("Accept", req.ExpectedMIME)
	}
	resp, err := client.Do(request)
	if err != nil {
		if len(req.PrivateQuery) > 0 {
			return OriginalRef{}, errors.New("authenticated original download failed")
		}
		return OriginalRef{}, err
	}
	defer resp.Body.Close()
	return s.acquireResponse(ctx, caller, req, resp)
}
func (s *OriginalService) acquireResponse(ctx context.Context, caller DatasetCatalogCaller, req AcquireRemoteOriginalRequest, resp *http.Response) (OriginalRef, error) {
	if resp.StatusCode != http.StatusOK {
		return OriginalRef{}, fmt.Errorf("original download returned HTTP %d", resp.StatusCode)
	}
	limit := originalMaxBytes
	if req.MaxBytes > 0 && req.MaxBytes < limit {
		limit = req.MaxBytes
	}
	if resp.ContentLength > limit {
		return OriginalRef{}, errors.New("original exceeds size limit")
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return OriginalRef{}, errors.New("encoded response cannot preserve original bytes")
	}
	name := req.DisplayName
	if name == "" {
		if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
			name = params["filename"]
		}
	}
	finalURL := req.URL
	if resp.Request != nil && resp.Request.URL != nil {
		final := *resp.Request.URL
		privateQuery := final.Query()
		for key := range req.PrivateQuery {
			privateQuery.Del(key)
		}
		final.RawQuery = privateQuery.Encode()
		finalURL = final.String()
		if name == "" {
			name = path.Base(resp.Request.URL.Path)
		}
	}
	if name == "." || name == "/" {
		name = "original"
	}
	actualMIME := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if name == "" || path.Ext(name) == "" {
		ext := map[string]string{"application/pdf": ".pdf", "text/plain": ".txt", "text/markdown": ".md", "text/html": ".html", "application/json": ".json"}[actualMIME]
		if ext == "" {
			return OriginalRef{}, errors.New("download filename or supported MIME required")
		}
		if name == "" {
			name = "original"
		}
		name += ext
	}
	expected := req.ExpectedMIME
	if expected == "" {
		expected = actualMIME
	}
	// HTML is a valid explicit web document, but cannot masquerade as a PDF or office original.
	ref, err := s.AcquireOriginal(ctx, caller, AcquireOriginalRequest{Filename: name, ContentType: expected, Reader: io.LimitReader(resp.Body, limit+1), FinalURL: finalURL})
	if err != nil {
		return OriginalRef{}, err
	}
	if ref.Size > limit || resp.ContentLength >= 0 && ref.Size != resp.ContentLength {
		s.discardOriginal(ctx, caller, ref.FileRef)
		return OriginalRef{}, io.ErrUnexpectedEOF
	}
	return ref, nil
}
func (s *OriginalService) discardOriginal(ctx context.Context, caller DatasetCatalogCaller, id string) {
	row, meta, err := s.resolve(ctx, s.db, caller, id)
	if err != nil {
		return
	}
	_ = s.db.WithContext(ctx).Delete(&row).Error
	_ = os.Remove(meta.StoredPath)
}
