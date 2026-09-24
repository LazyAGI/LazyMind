package builtin

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	skillpackage "lazymind/core/skillv2/skillpackage"
	skillpatch "lazymind/core/skillv2/skillpatch"
)

func TestAcquirePackageDownloadsOnceAndCachesVerifiedArchive(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "user-cache")
	archivePath := filepath.Join(root, "source.zip")
	files := map[string]string{
		"SKILL.md":       "---\nname: demo\ndescription: demo\n---\n# Demo\n",
		"scripts/run.py": "print('ok')\n",
	}
	writeCatalogZip(t, archivePath, files)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	entry := testRemoteEntry(server.URL+"/skill.zip", body, files)
	catalogPath := filepath.Join(root, "catalog.json")
	writeCatalog(t, catalogPath, entry)

	// Directory read must work before download and must not make a request.
	preview, found, err := catalogPackageByUID(catalogPath, entry.UID)
	if err != nil || !found || preview.ArchivePath != "" || len(preview.Files) != 1 || requests.Load() != 0 {
		t.Fatalf("preview = %#v, found=%v, err=%v, requests=%d", preview, found, err, requests.Load())
	}

	const callers = 6
	var wg sync.WaitGroup
	wg.Add(callers)
	errors := make(chan error, callers)
	for range callers {
		go func() {
			defer wg.Done()
			pkg, err := catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
			if err != nil {
				errors <- err
				return
			}
			if string(pkg.Files["scripts/run.py"]) != files["scripts/run.py"] {
				errors <- fmt.Errorf("installed package is incomplete")
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("download requests = %d, want 1", got)
	}
	if _, err := verifiedPackage(entry, cachePathForEntry(cacheRoot, entry)); err != nil {
		t.Fatalf("cached package: %v", err)
	}
	_, err = catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
	if err != nil || requests.Load() != 1 {
		t.Fatalf("cached re-acquire err=%v, requests=%d", err, requests.Load())
	}
	if err := os.WriteFile(cachePathForEntry(cacheRoot, entry), append(body, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
	if err != nil || requests.Load() != 2 {
		t.Fatalf("corrupt cache repair err=%v, requests=%d", err, requests.Load())
	}
}

func TestAcquirePackageRejectsChangedLockedVersionAndDoesNotCache(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "source.zip")
	files := map[string]string{"SKILL.md": "---\nname: demo\ndescription: demo\n---\n# Demo\n"}
	writeCatalogZip(t, archivePath, files)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(append(body, 'x'))
	}))
	defer server.Close()
	entry := testRemoteEntry(server.URL+"/skill.zip", body, files)
	catalogPath := filepath.Join(root, "catalog.json")
	writeCatalog(t, catalogPath, entry)
	cacheRoot := filepath.Join(root, "user-cache")
	_, err = catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
	if err == nil || !strings.Contains(err.Error(), "update the catalog") || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("locked mismatch error = %v", err)
	}
	if _, err := os.Stat(cachePathForEntry(cacheRoot, entry)); !os.IsNotExist(err) {
		t.Fatalf("mismatched archive was cached: %v", err)
	}
}

func TestAcquirePackageAppliesOnlyLockedPatchSet(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "user-cache")
	origin := map[string]string{"SKILL.md": "# Upstream\n", "script.py": "print('upstream')\n"}
	originPath := filepath.Join(root, "origin.zip")
	writeCatalogZip(t, originPath, origin)
	originBody, err := os.ReadFile(originPath)
	if err != nil {
		t.Fatal(err)
	}
	originFiles := map[string][]byte{"SKILL.md": []byte(origin["SKILL.md"]), "script.py": []byte(origin["script.py"])}
	newSkill := "---\nname: demo\ndescription: patched\n---\n# Upstream\n"
	patchRoot := filepath.Join(root, "patches")
	patchDir := filepath.Join(patchRoot, "demo", "add-frontmatter")
	if err := os.MkdirAll(filepath.Join(patchDir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(patchRoot, "catalog.yaml"), "schema_version: 1\npatches:\n  - demo/add-frontmatter/patch.yaml\n")
	writeTestFile(t, filepath.Join(patchDir, "files", "SKILL.md"), newSkill)
	before := sha256.Sum256(originFiles["SKILL.md"])
	writeTestFile(t, filepath.Join(patchDir, "patch.yaml"), fmt.Sprintf("schema_version: 1\nid: demo/add-frontmatter\ntarget:\n  uid: bsk_demo\n  version: 1.0.0\n  origin_tree_sha256: %s\noperations:\n  - op: upsert\n    path: SKILL.md\n    file: files/SKILL.md\n    before_sha256: %s\n", skillpackage.TreeHash(originFiles), hex.EncodeToString(before[:])))
	patchCatalog, err := skillpatch.LoadCatalog(filepath.Join(patchRoot, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	patched, err := skillpatch.Apply(skillpatch.Target{UID: "bsk_demo", Version: "1.0.0", OriginTreeHash: skillpackage.TreeHash(originFiles)}, originFiles, patchCatalog)
	if err != nil {
		t.Fatal(err)
	}
	finalPath, err := skillpackage.WriteZip(patched.Files, root)
	if err != nil {
		t.Fatal(err)
	}
	finalBody, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(originBody)
	}))
	defer server.Close()
	entry := testRemoteEntry(server.URL+"/skill.zip", finalBody, map[string]string{"SKILL.md": newSkill, "script.py": origin["script.py"]})
	entry.OriginArchiveSHA256 = digestHex(originBody)
	entry.OriginArchiveSize = int64(len(originBody))
	entry.OriginTreeSHA256 = skillpackage.TreeHash(originFiles)
	entry.PatchSetSHA256 = patched.PatchSetSHA256
	entry.AppliedPatches = []CatalogPatch{{ID: patched.AppliedPatches[0].ID, SHA256: patched.AppliedPatches[0].SHA256}}
	catalogPath := filepath.Join(root, "catalog.json")
	writeCatalog(t, catalogPath, entry)
	pkg, err := catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
	if err != nil || string(pkg.Files["SKILL.md"]) != newSkill {
		t.Fatalf("patched package = %#v, err=%v", pkg, err)
	}

	if err := os.Remove(cachePathForEntry(cacheRoot, entry)); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(patchDir, "files", "SKILL.md"), newSkill+"changed")
	_, err = catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
	if err == nil || !strings.Contains(err.Error(), "patch") {
		t.Fatalf("changed patch error = %v", err)
	}
}

func TestSelectOriginFindsLockedGitHubSubdirectory(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "repository.zip")
	writeCatalogZip(t, archivePath, map[string]string{
		"repo-main/skills/demo/SKILL.md": "# Demo\n",
		"repo-main/other/SKILL.md":       "# Other\n",
	})
	files := map[string][]byte{"SKILL.md": []byte("# Demo\n")}
	expectedPath, err := skillpackage.WriteZip(files, root)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	selected, actualFiles, err := selectOrigin(archivePath, []string{"wrong", "skills/demo"}, root, digestHex(expected), int64(len(expected)), skillpackage.TreeHash(files))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(selected)
	if string(actualFiles["SKILL.md"]) != "# Demo\n" {
		t.Fatalf("selected files = %#v", actualFiles)
	}
}

func TestLockedDownloadSourceUsesPinnedGitHubPathAndRejectsHTTP(t *testing.T) {
	const revision = "798e9b1a2bcac164d4f0c781908199e754f0bab6"
	entry := CatalogSkill{
		UID:         "bsk_demo",
		SourceURL:   "https://github.com/example/skills/tree/" + revision + "/skills/demo",
		ResolvedURL: "https://github.com/example/skills/archive/" + revision + ".zip",
	}
	downloadURL, prefixes, err := lockedDownloadSource(entry)
	if err != nil || downloadURL != entry.ResolvedURL || len(prefixes) == 0 || prefixes[0] != "skills/demo" {
		t.Fatalf("locked GitHub source = %q, %#v, %v", downloadURL, prefixes, err)
	}
	entry.ResolvedURL = "http://127.0.0.1/private.zip"
	if _, _, err := lockedDownloadSource(entry); err == nil {
		t.Fatal("insecure locked download URL was accepted")
	}
}

func TestAcquirePackageCancellationStopsDownloadAndLeavesNoCache(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "source.zip")
	files := map[string]string{"SKILL.md": "# Demo\n"}
	writeCatalogZip(t, archivePath, files)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	entry := testRemoteEntry(server.URL+"/skill.zip", body, files)
	catalogPath := filepath.Join(root, "catalog.json")
	writeCatalog(t, catalogPath, entry)
	cacheRoot := filepath.Join(root, "user-cache")
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := catalogAcquirePackageByUID(ctx, catalogPath, entry.UID, cacheRoot, server.Client())
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled acquire error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled acquire did not return")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("download request was not canceled")
	}
	if _, err := os.Stat(cachePathForEntry(cacheRoot, entry)); !os.IsNotExist(err) {
		t.Fatalf("canceled package was cached: %v", err)
	}
}

func TestAcquirePackageFirstCallerCancellationKeepsSharedDownload(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "source.zip")
	files := map[string]string{"SKILL.md": "# Demo\n"}
	writeCatalogZip(t, archivePath, files)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	serverCanceled := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		close(started)
		select {
		case <-release:
			_, _ = w.Write(body)
		case <-request.Context().Done():
			close(serverCanceled)
		}
	}))
	defer server.Close()
	entry := testRemoteEntry(server.URL+"/skill.zip", body, files)
	catalogPath := filepath.Join(root, "catalog.json")
	writeCatalog(t, catalogPath, entry)
	cacheRoot := filepath.Join(root, "user-cache")
	key := catalogPath + "\x00" + cacheRoot + "\x00" + entry.UID + "\x00" + entry.ArchiveSHA256
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := catalogAcquirePackageByUID(firstCtx, catalogPath, entry.UID, cacheRoot, server.Client())
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first download did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		pkg, err := catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
		if err == nil && string(pkg.Files["SKILL.md"]) != files["SKILL.md"] {
			err = fmt.Errorf("second caller got incomplete package")
		}
		secondDone <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		acquisitions.Lock()
		call := acquisitions.active[key]
		waiters := 0
		if call != nil {
			waiters = call.waiters
		}
		acquisitions.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second caller did not join shared download")
		}
		time.Sleep(time.Millisecond)
	}
	cancelFirst()
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first caller cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first caller did not cancel promptly")
	}
	if _, err := os.Stat(cachePathForEntry(cacheRoot, entry)); !os.IsNotExist(err) {
		t.Fatalf("archive committed before download completed: %v", err)
	}
	close(release)
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second caller did not finish")
	}
	select {
	case <-serverCanceled:
		t.Fatal("first caller canceled the shared download")
	default:
	}
	if requests.Load() != 1 {
		t.Fatalf("shared requests = %d, want 1", requests.Load())
	}
	if _, err := verifiedPackage(entry, cachePathForEntry(cacheRoot, entry)); err != nil {
		t.Fatalf("atomic cached archive: %v", err)
	}
}

func TestAcquirePackageIgnoresLegacyBundledRemoteArchive(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "packages", "demo.zip")
	files := map[string]string{"SKILL.md": "# Demo\n"}
	writeCatalogZip(t, archivePath, files)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	entry := testRemoteEntry(server.URL+"/skill.zip", body, files)
	catalogPath := filepath.Join(root, "catalog.json")
	writeCatalog(t, catalogPath, entry)
	cacheRoot := filepath.Join(root, "user-cache")
	if _, found, err := cachedPackage(catalogPath, entry, cacheRoot); err != nil || found {
		t.Fatalf("legacy remote ZIP should not count as cache: found=%v err=%v", found, err)
	}
	pkg, err := catalogAcquirePackageByUID(context.Background(), catalogPath, entry.UID, cacheRoot, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || pkg.ArchivePath != cachePathForEntry(cacheRoot, entry) {
		t.Fatalf("legacy remote ZIP bypassed download: requests=%d archive=%q", requests.Load(), pkg.ArchivePath)
	}
}

func TestDownloadClientRejectsPrivateHTTPSDestinationAndRedirect(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := downloadClient()
	defer client.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/private.zip", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(request)
	if err == nil || !strings.Contains(err.Error(), "non-public") || requests.Load() != 0 {
		t.Fatalf("private HTTPS destination: err=%v requests=%d", err, requests.Load())
	}
	redirectURL, err := url.Parse(server.URL + "/redirected.zip")
	if err != nil {
		t.Fatal(err)
	}
	err = client.CheckRedirect(&http.Request{URL: redirectURL}, []*http.Request{request})
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("private HTTPS redirect error = %v", err)
	}
	publicCDN, err := url.Parse("https://cdn.example.com/download.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(&http.Request{URL: publicCDN}, []*http.Request{request}); err != nil {
		t.Fatalf("public HTTPS CDN redirect was rejected: %v", err)
	}
}

func TestDownloadDialPinsPublicIPAndSkipsPrivateDNSAnswers(t *testing.T) {
	resolver := staticDownloadResolver{addresses: []netip.Addr{
		netip.MustParseAddr("10.1.2.3"),
		netip.MustParseAddr("100.100.100.200"),
		netip.MustParseAddr("8.8.8.8"),
	}}
	dialer := &recordingDownloadDialer{}
	conn, err := dialPublicDownloadAddress(context.Background(), "tcp", "example.com:443", resolver, dialer)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if len(dialer.addresses) != 1 || dialer.addresses[0] != "8.8.8.8:443" {
		t.Fatalf("dialed addresses = %#v", dialer.addresses)
	}
	for _, address := range []string{"127.0.0.1", "::ffff:127.0.0.1", "10.1.2.3", "100.100.100.200", "169.254.169.254", "fc00::1", "fe80::1", "64:ff9b::a00:1", "2002:0a00:0101::"} {
		if isPublicDownloadIP(netip.MustParseAddr(address)) {
			t.Errorf("private address %s accepted", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !isPublicDownloadIP(netip.MustParseAddr(address)) {
			t.Errorf("public address %s rejected", address)
		}
	}
	for _, rawURL := range []string{"https://localhost/", "https://metadata.google.internal/", "https://100.100.100.200/", "https://[::ffff:127.0.0.1]/"} {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateDownloadURL(parsed); err == nil {
			t.Errorf("non-public URL %q accepted", rawURL)
		}
	}
}

func TestDownloadClientRetainsHostnameForTLS(t *testing.T) {
	serverName := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		serverName <- request.TLS.ServerName
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := downloadClient()
	router := client.Transport.(*downloadTransport)
	router.proxyFor = func(*http.Request) (*url.URL, error) { return nil, nil }
	transport := router.direct
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	// The test server certificate does not cover this synthetic public host.
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	defer client.CloseIdleConnections()
	response, err := client.Get("https://example.com/test")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	select {
	case actual := <-serverName:
		if actual != "example.com" {
			t.Fatalf("TLS SNI = %q", actual)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TLS request did not reach server")
	}
}

func TestDownloadClientUsesConfiguredProxyForPublicHost(t *testing.T) {
	var connects atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect || request.Host != "example.com:443" {
			t.Errorf("proxy request = %s %s", request.Method, request.Host)
		}
		connects.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxyServer.Close()
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := downloadClient()
	defer client.CloseIdleConnections()
	router := client.Transport.(*downloadTransport)
	router.proxyFor = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	router.proxied.Proxy = router.proxyFor
	router.direct.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, catalogFailure("direct download path was used")
	}
	_, err = client.Get("https://example.com/download.zip")
	if err == nil || connects.Load() != 1 {
		t.Fatalf("configured proxy was not used: err=%v CONNECTs=%d", err, connects.Load())
	}
	_, err = client.Get("https://127.0.0.1/private.zip")
	if err == nil || !strings.Contains(err.Error(), "non-public") || connects.Load() != 1 {
		t.Fatalf("private target reached proxy: err=%v CONNECTs=%d", err, connects.Load())
	}
}

type staticDownloadResolver struct {
	addresses []netip.Addr
}

func (resolver staticDownloadResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return resolver.addresses, nil
}

type recordingDownloadDialer struct {
	addresses []string
}

func (dialer *recordingDownloadDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	dialer.addresses = append(dialer.addresses, address)
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func testRemoteEntry(sourceURL string, body []byte, files map[string]string) CatalogSkill {
	packageFiles := make(map[string][]byte, len(files))
	for path, content := range files {
		packageFiles[path] = []byte(content)
	}
	return CatalogSkill{
		UID: "bsk_demo", Key: "demo", SourceURL: sourceURL, ResolvedURL: sourceURL,
		Version: "1.0.0", Name: "demo", Description: "demo", Category: "external",
		Content: files["SKILL.md"], ArchiveSHA256: digestHex(body),
		TreeSHA256: skillpackage.TreeHash(packageFiles), ArchiveSize: int64(len(body)), PackageFile: "packages/demo.zip",
	}
}

func digestHex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
