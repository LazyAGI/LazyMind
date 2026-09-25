package builtin

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	skillpackage "lazymind/core/skillv2/skillpackage"
	skillpatch "lazymind/core/skillv2/skillpatch"
	skillurl "lazymind/core/skillv2/sourceurl"
)

const (
	cacheDirectoryEnv = "LAZYMIND_BUILTIN_SKILL_CACHE"
	maxDownloadBytes  = 64 << 20
)

type acquisitionCall struct {
	done         chan struct{}
	cancel       context.CancelFunc
	waiters      int
	packageValue Package
	err          error
}

var acquisitions = struct {
	sync.Mutex
	active map[string]*acquisitionCall
}{active: make(map[string]*acquisitionCall)}

// AcquirePackageByUID downloads a locked builtin Skill only when its complete
// archive is not already available locally. The catalog and its package
// snapshots remain usable without network access.
func AcquirePackageByUID(ctx context.Context, uid string) (*Package, error) {
	catalogPath := CatalogPath()
	if catalogPath == "" {
		return nil, catalogFailure("builtin Skill catalog is unavailable")
	}
	cacheRoot, err := builtinCacheRoot()
	if err != nil {
		return nil, err
	}
	return catalogAcquirePackageByUID(ctx, catalogPath, strings.TrimSpace(uid), cacheRoot, downloadClient())
}

// CachedPackageByUID returns a complete, verified archive if it is already on
// disk. A catalog snapshot alone is not a cached package.
func CachedPackageByUID(uid string) (Package, bool, error) {
	catalogPath := CatalogPath()
	if catalogPath == "" {
		return Package{}, false, nil
	}
	cacheRoot, err := builtinCacheRoot()
	if err != nil {
		return Package{}, false, err
	}
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return Package{}, false, err
	}
	for _, entry := range catalog.Skills {
		if entry.UID == strings.TrimSpace(uid) {
			return cachedPackage(catalogPath, entry, cacheRoot)
		}
	}
	return Package{}, false, nil
}

func builtinCacheRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(cacheDirectoryEnv)); configured != "" {
		return filepath.Abs(configured)
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		return "", catalogFailure("find user cache directory: %v", err)
	}
	return filepath.Join(userCache, "lazymind", "builtin-skills"), nil
}

func cachePathForEntry(cacheRoot string, entry CatalogSkill) string {
	return filepath.Join(cacheRoot, entry.ArchiveSHA256+".zip")
}

func cachedPackage(catalogPath string, entry CatalogSkill, cacheRoot string) (Package, bool, error) {
	if strings.HasPrefix(entry.SourceURL, "builtin://") {
		bundledPath, err := resolvePackagePath(catalogPath, entry.PackageFile)
		if err != nil {
			return Package{}, false, err
		}
		if exists, err := regularFileExists(bundledPath); err != nil {
			return Package{}, false, catalogFailure("builtin Skill %s packaged archive: %v", entry.UID, err)
		} else if exists {
			pkg, err := verifiedPackage(entry, bundledPath)
			return pkg, err == nil, err
		}
	}
	if cacheRoot == "" {
		return Package{}, false, nil
	}
	cachePath := cachePathForEntry(cacheRoot, entry)
	if exists, err := regularFileExists(cachePath); err != nil || !exists {
		// A corrupt user cache must not break catalog browsing or background
		// status checks. An explicit acquisition can replace it.
		return Package{}, false, nil
	}
	pkg, err := verifiedPackage(entry, cachePath)
	if err != nil {
		return Package{}, false, nil
	}
	return pkg, true, nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, catalogFailure("archive is not a regular file: %s", path)
	}
	return true, nil
}

func verifiedPackage(entry CatalogSkill, archivePath string) (Package, error) {
	if err := verifyArchive(archivePath, entry.ArchiveSHA256, entry.ArchiveSize); err != nil {
		return Package{}, err
	}
	archive, err := skillpackage.ReadZip(archivePath)
	if err != nil {
		return Package{}, err
	}
	if _, ok := archive.Files["SKILL.md"]; !ok {
		return Package{}, catalogFailure("builtin Skill %s archive is missing SKILL.md", entry.UID)
	}
	if actual := skillpackage.TreeHash(archive.Files); actual != entry.TreeSHA256 {
		return Package{}, catalogFailure("builtin Skill %s archive tree sha256 mismatch: got %s, want %s", entry.UID, actual, entry.TreeSHA256)
	}
	return packageFromCatalog(entry, archivePath, archive.Files), nil
}

func catalogAcquirePackageByUID(ctx context.Context, catalogPath, uid, cacheRoot string, client *http.Client) (*Package, error) {
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	var entry *CatalogSkill
	for i := range catalog.Skills {
		if catalog.Skills[i].UID == uid {
			entry = &catalog.Skills[i]
			break
		}
	}
	if entry == nil {
		return nil, catalogFailure("builtin Skill %s was not found", uid)
	}
	key := catalogPath + "\x00" + cacheRoot + "\x00" + entry.UID + "\x00" + entry.ArchiveSHA256
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	acquisitions.Lock()
	call := acquisitions.active[key]
	if call == nil {
		sharedCtx, cancel := context.WithCancel(context.Background())
		call = &acquisitionCall{done: make(chan struct{}), cancel: cancel}
		acquisitions.active[key] = call
		go func() {
			pkg, err := acquireLockedPackage(sharedCtx, catalogPath, *entry, cacheRoot, client)
			acquisitions.Lock()
			call.packageValue, call.err = pkg, err
			if acquisitions.active[key] == call {
				delete(acquisitions.active, key)
			}
			close(call.done)
			acquisitions.Unlock()
			cancel()
		}()
	}
	call.waiters++
	acquisitions.Unlock()
	select {
	case <-ctx.Done():
		acquisitions.Lock()
		call.waiters--
		if call.waiters == 0 && acquisitions.active[key] == call {
			delete(acquisitions.active, key)
			call.cancel()
		}
		acquisitions.Unlock()
		return nil, ctx.Err()
	case <-call.done:
		if call.err != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, catalogFailure("download builtin Skill %s failed; retry or update the catalog: %v", uid, call.err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pkg := call.packageValue
		return &pkg, nil
	}
}

func acquireLockedPackage(ctx context.Context, catalogPath string, entry CatalogSkill, cacheRoot string, client *http.Client) (Package, error) {
	if pkg, found, err := cachedPackage(catalogPath, entry, cacheRoot); err != nil || found {
		return pkg, err
	}
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return Package{}, catalogFailure("create builtin Skill cache: %v", err)
	}
	resolvedURL, pathPrefixes, err := lockedDownloadSource(entry)
	if err != nil {
		return Package{}, err
	}
	downloaded, err := downloadArchive(ctx, client, resolvedURL, cacheRoot)
	if err != nil {
		return Package{}, err
	}
	defer os.Remove(downloaded)

	expectedOriginHash, expectedOriginSize, expectedOriginTree := lockedOrigin(entry)
	originPath, originFiles, err := selectOrigin(downloaded, pathPrefixes, cacheRoot, expectedOriginHash, expectedOriginSize, expectedOriginTree)
	if err != nil {
		return Package{}, err
	}
	if originPath != downloaded {
		defer os.Remove(originPath)
	}
	finalPath := originPath
	if len(entry.AppliedPatches) > 0 {
		patchCatalogPath := filepath.Join(filepath.Dir(catalogPath), "patches", "catalog.yaml")
		patches, err := skillpatch.LoadCatalog(patchCatalogPath)
		if err != nil {
			return Package{}, catalogFailure("load builtin Skill patches: %v", err)
		}
		patched, err := skillpatch.Apply(skillpatch.Target{
			UID: entry.UID, Version: entry.Version, OriginTreeHash: expectedOriginTree,
		}, originFiles, patches)
		if err != nil {
			return Package{}, err
		}
		if err := validateAppliedPatches(entry, patched); err != nil {
			return Package{}, err
		}
		finalPath, err = skillpackage.WriteZip(patched.Files, cacheRoot)
		if err != nil {
			return Package{}, err
		}
		defer os.Remove(finalPath)
	}
	if _, err := verifiedPackage(entry, finalPath); err != nil {
		return Package{}, catalogFailure("locked final package mismatch: %v", err)
	}
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	cachePath := cachePathForEntry(cacheRoot, entry)
	if err := os.Rename(finalPath, cachePath); err != nil {
		if pkg, found, _ := cachedPackage(catalogPath, entry, cacheRoot); found {
			return pkg, nil
		}
		return Package{}, catalogFailure("save builtin Skill cache: %v", err)
	}
	return verifiedPackage(entry, cachePath)
}

func lockedOrigin(entry CatalogSkill) (hash string, size int64, tree string) {
	if len(entry.AppliedPatches) != 0 {
		return entry.OriginArchiveSHA256, entry.OriginArchiveSize, entry.OriginTreeSHA256
	}
	return entry.ArchiveSHA256, entry.ArchiveSize, entry.TreeSHA256
}

func validateAppliedPatches(entry CatalogSkill, patched skillpatch.Result) error {
	if len(patched.AppliedPatches) != len(entry.AppliedPatches) || patched.PatchSetSHA256 != entry.PatchSetSHA256 {
		return catalogFailure("builtin Skill %s patch set does not match locked catalog", entry.UID)
	}
	for i, applied := range patched.AppliedPatches {
		if applied.ID != entry.AppliedPatches[i].ID || applied.SHA256 != entry.AppliedPatches[i].SHA256 {
			return catalogFailure("builtin Skill %s patch %s does not match locked catalog", entry.UID, applied.ID)
		}
	}
	return nil
}

func lockedDownloadSource(entry CatalogSkill) (string, []string, error) {
	resolved, err := url.Parse(entry.ResolvedURL)
	if err != nil || resolved.Hostname() == "" || resolved.User != nil || resolved.Fragment != "" || resolved.Scheme != "https" {
		return "", nil, catalogFailure("builtin Skill %s has an invalid locked download URL", entry.UID)
	}
	source, err := url.Parse(entry.SourceURL)
	if err != nil {
		return "", nil, err
	}
	github, matched, err := skillurl.ResolveGitHubPageURLFromResolvedArchive(source, entry.ResolvedURL)
	if err != nil {
		return "", nil, catalogFailure("locked GitHub source: %v", err)
	}
	if matched {
		if github.DownloadURL != entry.ResolvedURL {
			return "", nil, catalogFailure("builtin Skill %s locked GitHub URL changed", entry.UID)
		}
		if github.PathPrefix == "" {
			return entry.ResolvedURL, nil, nil
		}
		return entry.ResolvedURL, append([]string{github.PathPrefix}, github.PathPrefixCandidates...), nil
	}
	if strings.EqualFold(resolved.Hostname(), "api.skillhub.cn") && entry.Version != "" && !strings.HasPrefix(entry.Version, "0.0.0+") {
		query := resolved.Query()
		query.Set("version", entry.Version)
		resolved.RawQuery = query.Encode()
	}
	return resolved.String(), nil, nil
}

func selectOrigin(downloaded string, prefixes []string, cacheRoot, expectedHash string, expectedSize int64, expectedTree string) (string, map[string][]byte, error) {
	if len(prefixes) == 0 {
		if err := verifyArchive(downloaded, expectedHash, expectedSize); err != nil {
			return "", nil, catalogFailure("locked origin package mismatch: %v", err)
		}
		pkg, err := skillpackage.ReadZip(downloaded)
		if err != nil {
			return "", nil, err
		}
		if actual := skillpackage.TreeHash(pkg.Files); actual != expectedTree {
			return "", nil, catalogFailure("locked origin tree sha256 mismatch: got %s, want %s", actual, expectedTree)
		}
		return downloaded, pkg.Files, nil
	}
	seen := make(map[string]bool, len(prefixes))
	for _, prefix := range prefixes {
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		pkg, err := skillpackage.ReadZipSubdirectory(downloaded, prefix)
		if err != nil {
			continue
		}
		selected, err := skillpackage.WriteZip(pkg.Files, cacheRoot)
		if err != nil {
			return "", nil, err
		}
		if err := verifyArchive(selected, expectedHash, expectedSize); err != nil || skillpackage.TreeHash(pkg.Files) != expectedTree {
			_ = os.Remove(selected)
			continue
		}
		return selected, pkg.Files, nil
	}
	return "", nil, catalogFailure("GitHub Skill path does not match locked origin; update the catalog")
}

func downloadClient() *http.Client {
	direct := http.DefaultTransport.(*http.Transport).Clone()
	// Direct downloads dial a checked public IP rather than resolving the host
	// again. net/http still uses the URL hostname for TLS and the Host header.
	direct.Proxy = nil
	direct.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialPublicDownloadAddress(ctx, network, address, net.DefaultResolver, &net.Dialer{Timeout: 30 * time.Second})
	}
	proxied := http.DefaultTransport.(*http.Transport).Clone()
	proxied.Proxy = http.ProxyFromEnvironment
	return &http.Client{
		Transport: &downloadTransport{direct: direct, proxied: proxied, proxyFor: http.ProxyFromEnvironment},
		Timeout:   90 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return catalogFailure("too many download redirects")
			}
			return validateDownloadURL(request.URL)
		},
	}
}

type downloadTransport struct {
	direct   *http.Transport
	proxied  *http.Transport
	proxyFor func(*http.Request) (*url.URL, error)
}

func (transport *downloadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateDownloadURL(request.URL); err != nil {
		return nil, err
	}
	proxy, err := transport.proxyFor(request)
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		// An explicitly configured proxy is the administrator's DNS/network
		// boundary. Keep its normal CONNECT behavior for corporate networks.
		return transport.proxied.RoundTrip(request)
	}
	return transport.direct.RoundTrip(request)
}

func (transport *downloadTransport) CloseIdleConnections() {
	transport.direct.CloseIdleConnections()
	transport.proxied.CloseIdleConnections()
}

func validateDownloadURL(target *url.URL) error {
	if target == nil || target.Scheme != "https" || target.User != nil || target.Hostname() == "" || target.Fragment != "" {
		return catalogFailure("download URL must use HTTPS without credentials")
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicDownloadIP(ip) {
			return catalogFailure("download URL targets a non-public address")
		}
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || !strings.Contains(host, ".") {
		return catalogFailure("download URL targets a non-public host")
	}
	if strings.Trim(host, "0123456789.") == "" {
		return catalogFailure("download URL contains an invalid numeric address")
	}
	return nil
}

type downloadResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type downloadDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

var nonPublicDownloadRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),  // Shared address space, including cloud metadata hosts.
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments.
	netip.MustParsePrefix("192.0.2.0/24"),   // Documentation.
	netip.MustParsePrefix("192.88.99.0/24"), // Deprecated 6to4 relay.
	netip.MustParsePrefix("198.18.0.0/15"),  // Benchmarking.
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can tunnel to private IPv4.
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/23"),     // Protocol assignments, including Teredo.
	netip.MustParsePrefix("2001:db8::/32"), // Documentation.
	netip.MustParsePrefix("2002::/16"),     // 6to4 can tunnel to private IPv4.
}

func isPublicDownloadIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, blocked := range nonPublicDownloadRanges {
		if blocked.Contains(ip) {
			return false
		}
	}
	return true
}

func dialPublicDownloadAddress(ctx context.Context, network, address string, resolver downloadResolver, dialer downloadDialer) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, catalogFailure("invalid download address: %v", err)
	}
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{literal}
	} else {
		addresses, err = resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, catalogFailure("resolve download host failed: %v", err)
		}
	}
	allowed := false
	var lastErr error
	for _, ip := range addresses {
		if !isPublicDownloadIP(ip) {
			continue
		}
		allowed = true
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if !allowed {
		return nil, catalogFailure("download host resolves only to non-public addresses")
	}
	return nil, catalogFailure("connect to download host failed: %v", lastErr)
}

func downloadArchive(ctx context.Context, client *http.Client, rawURL, cacheRoot string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "LazyMind-Builtin-Skill/1")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", catalogFailure("download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxDownloadBytes {
		return "", catalogFailure("download exceeds %d bytes", maxDownloadBytes)
	}
	file, err := os.CreateTemp(cacheRoot, ".builtin-download-*.zip")
	if err != nil {
		return "", err
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	size, err := io.Copy(file, io.LimitReader(response.Body, maxDownloadBytes+1))
	if err != nil {
		return "", err
	}
	if size > maxDownloadBytes {
		return "", catalogFailure("download exceeds %d bytes", maxDownloadBytes)
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	keep = true
	return path, nil
}
