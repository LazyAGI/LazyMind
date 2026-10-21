package academic

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// WebDAV addresses may intentionally point at the user's NAS. Link-local,
// unspecified, multicast, and cloud-instance metadata addresses are never valid
// targets. Public Zotero file redirects additionally prohibit private addresses.
func zoteroAllowedIP(ip net.IP, allowPrivate bool) bool {
	if ip == nil || (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// EC2's IPv6 metadata endpoint is a private address, not link-local.
	// Explicit NAS configuration must not grant access to instance metadata.
	if ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return false
	}
	if v4 := ip.To4(); v4 != nil && (v4[0] == 0 || v4[0] >= 240 || (v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127)) {
		return false
	}
	if !allowPrivate && (ip.IsPrivate() || ip.IsLoopback()) {
		return false
	}
	return true
}

func zoteroAllowedURL(u *url.URL, allowPrivate bool) bool {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	if !allowPrivate && u.Scheme != "https" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !zoteroAllowedIP(ip, allowPrivate) {
		return false
	}
	if strings.EqualFold(u.Hostname(), "metadata.google.internal") || strings.EqualFold(u.Hostname(), "metadata.goog") {
		return false
	}
	return true
}

func zoteroTransport(allowPrivate bool) *http.Transport {
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second}
	// DNS checks must run on the actual destination, rather than on a proxy that
	// can resolve a forbidden target on our behalf.
	transport.Proxy = nil
	transport.MaxIdleConns = 10
	transport.MaxIdleConnsPerHost = 2
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, zoteroFailure(400, "INVALID_ZOTERO_TARGET", "文献来源地址无效")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, zoteroFailure(502, "ZOTERO_DNS_FAILED", "无法解析文献来源地址")
		}
		for _, ip := range ips {
			if !zoteroAllowedIP(ip.IP, allowPrivate) {
				return nil, zoteroFailure(400, "ZOTERO_TARGET_FORBIDDEN", "文献来源地址不可用于导入")
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return transport
}

// A WebDAV request may reach an explicitly configured private NAS, but an
// unrelated Zotero CDN redirect must still use public addresses. Do not relax
// the entire client's dial policy merely because WebDAV is enabled.
type zoteroRoutingTransport struct {
	client  *zoteroClient
	public  *http.Transport
	private *http.Transport
}

func (t *zoteroRoutingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if (t.client.config.Source == "local" && sameZoteroOrigin(r.URL, t.client.base)) || (t.client.dav != nil && sameZoteroOrigin(r.URL, t.client.dav)) {
		return t.private.RoundTrip(r)
	}
	return t.public.RoundTrip(r)
}

func (t *zoteroRoutingTransport) CloseIdleConnections() {
	t.public.CloseIdleConnections()
	t.private.CloseIdleConnections()
}

func sameZoteroOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (c *zoteroClient) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return zoteroFailure(502, "ZOTERO_REDIRECT_REJECTED", "文献来源重定向次数过多")
	}
	original := via[0].URL
	if c.config.Source == "local" {
		if req.URL.Scheme == "file" {
			return zoteroFailure(422, "LOCAL_ATTACHMENT_REQUIRED", "本地 Zotero 附件位于你的电脑，请在导入窗口选择 Zotero 的 storage 文件夹")
		}
		if !sameZoteroOrigin(req.URL, c.base) || req.URL.User != nil {
			return zoteroFailure(502, "ZOTERO_REDIRECT_REJECTED", "本地 Zotero 返回了不支持的附件地址")
		}
		return nil
	}
	if c.dav != nil && sameZoteroOrigin(original, c.dav) {
		if !sameZoteroOrigin(req.URL, c.dav) || !zoteroAllowedURL(req.URL, true) {
			return zoteroFailure(502, "ZOTERO_REDIRECT_REJECTED", "WebDAV 重定向到其他服务器，已停止以保护登录信息")
		}
		return nil
	}
	if !zoteroAllowedURL(req.URL, false) {
		return zoteroFailure(502, "ZOTERO_REDIRECT_REJECTED", "Zotero 返回了不安全的附件下载地址")
	}
	if !sameZoteroOrigin(req.URL, c.base) {
		if !strings.HasSuffix(original.Path, "/file") {
			return zoteroFailure(502, "ZOTERO_REDIRECT_REJECTED", "Zotero 题录接口重定向到非官方服务器")
		}
		req.Header.Del("Zotero-API-Key")
		req.Header.Del("Zotero-Server-ID")
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
	}
	return nil
}

func readZoteroZIP(data []byte, filename string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(r.File) > 1000 {
		return nil, zoteroFailure(502, "INVALID_ZOTERO_ARCHIVE", "WebDAV 附件不是有效的 Zotero ZIP 文件")
	}
	var selected *zip.File
	var total uint64
	for _, file := range r.File {
		name := file.Name
		// Zotero retains support for older WebDAV archives whose entry names
		// are Base64 followed by %ZB64 (storage/storageLocal.js). Validate the
		// decoded path before matching it; never extract arbitrary ZIP paths.
		if strings.HasSuffix(name, "%ZB64") {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(name, "%ZB64"))
			if err != nil {
				return nil, zoteroFailure(502, "INVALID_ZOTERO_ARCHIVE", "WebDAV 附件包含不安全的文件路径")
			}
			name = string(decoded)
		}
		clean := path.Clean(name)
		if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains("/"+name+"/", "/../") || file.Mode().Type() != 0 && !file.FileInfo().IsDir() {
			return nil, zoteroFailure(502, "INVALID_ZOTERO_ARCHIVE", "WebDAV 附件包含不安全的文件路径")
		}
		if file.UncompressedSize64 > zoteroMaxPDFBytes || total > zoteroMaxPDFBytes-file.UncompressedSize64 {
			return nil, zoteroFailure(413, "ZOTERO_ARCHIVE_TOO_LARGE", "WebDAV 附件解压后超过 128 MiB 上限")
		}
		total += file.UncompressedSize64
		if !file.FileInfo().IsDir() && clean == filename {
			if selected != nil {
				return nil, zoteroFailure(502, "INVALID_ZOTERO_ARCHIVE", "WebDAV 附件包含重复的 PDF 文件")
			}
			selected = file
		}
	}
	if selected == nil {
		return nil, zoteroFailure(404, "ZOTERO_ATTACHMENT_MISSING", "WebDAV 压缩包中没有 Zotero 记录对应的 PDF 附件")
	}
	reader, err := selected.Open()
	if err != nil {
		return nil, zoteroFailure(502, "INVALID_ZOTERO_ARCHIVE", "无法打开 WebDAV 附件")
	}
	defer reader.Close()
	pdf, err := io.ReadAll(io.LimitReader(reader, zoteroMaxPDFBytes+1))
	if err != nil {
		return nil, zoteroFailure(502, "INVALID_ZOTERO_ARCHIVE", "WebDAV 附件解压失败或校验错误")
	}
	if len(pdf) > zoteroMaxPDFBytes {
		return nil, zoteroFailure(413, "ZOTERO_ARCHIVE_TOO_LARGE", "WebDAV 附件解压后超过 128 MiB 上限")
	}
	return pdf, nil
}
