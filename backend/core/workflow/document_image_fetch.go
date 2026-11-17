package workflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"lazymind/core/doc"
)

const maxExportImageBytes int64 = 20 << 20
const maxExportTotalBytes int64 = 200 << 20

var imageNonPublicNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func imagePublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	for _, network := range imageNonPublicNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func validImageRemoteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil &&
		(u.Port() == "" || u.Port() == "80" || u.Port() == "443")
}

func imageExportHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{ResponseHeaderTimeout: 15 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("image unavailable")
			}
			for _, ip := range ips {
				if !imagePublicIP(ip) {
					return nil, errors.New("image unavailable")
				}
			}
			// Dial the checked IP, never resolve the hostname again after validation.
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		}}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !validImageRemoteURL(req.URL.String()) {
			return errors.New("image unavailable")
		}
		return nil
	}}
}

var errPublicImageTooLarge = errors.New("image download exceeds the size limit")

// DownloadPublicImage shares the document image export transport. It does not
// resolve server-local references or trust the response's claimed image type.
func DownloadPublicImage(ctx context.Context, source string, limit int64) ([]byte, error) {
	client := imageExportHTTPClient()
	defer client.CloseIdleConnections()
	return readPublicImage(ctx, client, source, limit)
}

func readPublicImage(ctx context.Context, client *http.Client, source string, limit int64) ([]byte, error) {
	if !validImageRemoteURL(source) {
		return nil, errors.New("image download requires a public HTTP(S) URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, errors.New("invalid image URL")
	}
	request.Header.Set("Accept", "image/*")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("image could not be downloaded")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, errPublicImageTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("image download was interrupted")
	}
	if int64(len(data)) > limit {
		return nil, errPublicImageTooLarge
	}
	return data, nil
}

func fetchDocumentImage(ctx context.Context, client *http.Client, owner string, item imageExportSource) ([]byte, string) {
	var data []byte
	var err error
	switch {
	case item.Local:
		data, err = doc.ReadDocumentImage(ctx, item.Source, owner, maxExportImageBytes)
	case strings.HasPrefix(item.Source, "data:image/"):
		parts := strings.SplitN(item.Source, ",", 2)
		if len(parts) != 2 || !strings.HasSuffix(parts[0], ";base64") {
			return nil, "IMAGE_UNAVAILABLE"
		}
		data, err = io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(parts[1])), maxExportImageBytes+1))
	case validImageRemoteURL(item.Source):
		data, err = readPublicImage(ctx, client, item.Source, maxExportImageBytes)
		if errors.Is(err, errPublicImageTooLarge) {
			return nil, "IMAGE_TOO_LARGE"
		}
	default:
		return nil, "IMAGE_UNAVAILABLE"
	}
	if err != nil {
		return nil, "IMAGE_UNAVAILABLE"
	}
	if int64(len(data)) > maxExportImageBytes {
		return nil, "IMAGE_TOO_LARGE"
	}
	if imageFileExtension(data) == "" {
		return nil, "IMAGE_FORMAT_UNSUPPORTED"
	}
	if item.ExpectedHash != "" && imageExportHash(data) != item.ExpectedHash {
		return nil, "IMAGE_CHANGED"
	}
	return data, ""
}

func imageFileExtension(data []byte) string {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/x-icon":
		return ".ico"
	case "image/tiff":
		return ".tiff"
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" && (string(data[8:12]) == "avif" || string(data[8:12]) == "avis") {
		return ".avif"
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		if _, ok := token.(xml.Directive); ok {
			return ""
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Local == "svg" {
				return ".svg"
			}
			return ""
		}
	}
}
