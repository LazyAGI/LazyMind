package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"lazymind/core/skillv2/httperr"
)

const packageCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type packageHTTPFixture struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	requests []string
	routes   map[string]any
	raw      map[string]string
}

func newPackageHTTPFixture(t *testing.T, routes map[string]any, raw map[string]string) *packageHTTPFixture {
	t.Helper()
	fixture := &packageHTTPFixture{t: t, routes: routes, raw: raw}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, r.URL.RequestURI())
		fixture.mu.Unlock()
		key := r.URL.RequestURI()
		if value, ok := fixture.routes[key]; ok {
			if handler, ok := value.(http.HandlerFunc); ok {
				handler(w, r)
				return
			}
			if status, ok := value.(int); ok {
				w.WriteHeader(status)
				return
			}
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		if value, ok := fixture.raw[r.URL.Path]; ok {
			_, _ = io.WriteString(w, value)
			return
		}
		t.Errorf("unexpected request: %s", key)
		http.NotFound(w, r)
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *packageHTTPFixture) downloader() HTTPZipDownloader {
	return HTTPZipDownloader{client: f.server.Client(), githubAPIBaseURL: f.server.URL + "/api", githubRawBaseURL: f.server.URL + "/raw"}
}

func packageTree(entries ...map[string]any) map[string]any {
	return map[string]any{"sha": "tree-sha", "truncated": false, "tree": entries}
}
func packageEntry(name, mode, kind, sha string, size int64) map[string]any {
	return map[string]any{"path": name, "mode": mode, "type": kind, "sha": sha, "size": size, "url": "https://api.github.com/git/ignored"}
}
func packageSource(path string) SourceInput {
	source := SourceInput{URL: "https://github.com/acme/repo/archive/" + packageCommit + ".zip", SourceURL: "https://github.com/acme/repo"}
	if path != "" {
		source.SourceURL += "/tree/main/" + path
		source.PathPrefix = path
	}
	return source
}
func packageZipContents(t *testing.T, downloaded DownloadedZip) map[string]string {
	t.Helper()
	if downloaded.Cleanup != nil {
		t.Cleanup(downloaded.Cleanup)
	}
	r, err := zip.OpenReader(downloaded.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	files := map[string]string{}
	for _, entry := range r.File {
		rc, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name] = string(data)
	}
	return files
}

func TestGitHubDirectoryDownloadContainsOnlySelectedSkill(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit:        packageTree(packageEntry("skills", "040000", "tree", "skills-tree", 0), packageEntry("huge.bin", "100644", "blob", "huge", 500<<20)),
		"/api/repos/acme/repo/git/trees/skills-tree":             packageTree(packageEntry("target", "040000", "tree", "target-tree", 0), packageEntry("sibling", "040000", "tree", "sibling-tree", 0)),
		"/api/repos/acme/repo/git/trees/target-tree?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 5), packageEntry("scripts", "040000", "tree", "scripts", 0), packageEntry("scripts/run.sh", "100755", "blob", "script", 3)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/skills/target/SKILL.md": "skill", "/raw/acme/repo/" + packageCommit + "/skills/target/scripts/run.sh": "run"})
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), packageSource("skills/target"))
	if err != nil {
		t.Fatal(err)
	}
	if downloaded.PathPrefix == nil || *downloaded.PathPrefix != "" {
		t.Fatalf("target archive path override=%v", downloaded.PathPrefix)
	}
	if downloaded.PackageRoot != "target" {
		t.Fatalf("package root=%q", downloaded.PackageRoot)
	}
	if got := packageZipContents(t, downloaded); !reflect.DeepEqual(got, map[string]string{"SKILL.md": "skill", "scripts/run.sh": "run"}) {
		t.Fatalf("zip contents=%v", got)
	}
}

func TestGitHubRepositoryDownloadSelectsUniqueSkillDirectory(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("README.md", "100644", "blob", "readme", 2), packageEntry("plugins/weather/skill.MD", "100644", "blob", "skill", 5), packageEntry("plugins/weather/data.txt", "100644", "blob", "data", 4), packageEntry("other.bin", "100644", "blob", "big", 900<<20)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/plugins/weather/skill.MD": "skill", "/raw/acme/repo/" + packageCommit + "/plugins/weather/data.txt": "data"})
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), packageSource(""))
	if err != nil {
		t.Fatal(err)
	}
	if got := packageZipContents(t, downloaded); !reflect.DeepEqual(got, map[string]string{"skill.MD": "skill", "data.txt": "data"}) {
		t.Fatalf("zip contents=%v", got)
	}
}

func TestGitHubRepositoryDownloadRejectsMultipleSkillsWithoutArchiveFallback(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("one/SKILL.md", "100644", "blob", "one", 1), packageEntry("two/SKILL.md", "100644", "blob", "two", 1)),
	}, nil)
	_, err := fixture.downloader().DownloadSource(context.Background(), packageSource(""))
	if err == nil {
		t.Fatal("expected multiple skills error")
	}
	sem := httperr.ForError(err)
	if sem.Status != 400 || sem.Code != "github_multiple_skills" || !strings.Contains(err.Error(), "specific") || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("error=%+v", sem)
	}
}

func TestGitHubRepositoryDownloadRejectsMissingSkillWithoutFallback(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("README.md", "100644", "blob", "readme", 2)),
	}, nil)
	_, err := fixture.downloader().DownloadSource(context.Background(), packageSource(""))
	if err == nil {
		t.Fatal("expected missing skill error")
	}
	if sem := httperr.ForError(err); sem.Status != 422 || sem.Code != httperr.CodeSkillMDNotFound {
		t.Fatalf("error=%+v", sem)
	}
}

func TestGitHubDirectoryDownloadRejectsUnsafeAndOversizedEntries(t *testing.T) {
	tests := []struct {
		name   string
		extra  map[string]any
		want   string
		status int
	}{
		{"symlink", packageEntry("link", "120000", "blob", "link", 1), "symlink", 400},
		{"submodule", packageEntry("module", "160000", "commit", "module", 0), "submodule", 400},
		{"traversal", packageEntry("../escape", "100644", "blob", "escape", 1), "unsafe path", 400},
		{"backslash", packageEntry(`dir\escape`, "100644", "blob", "escape", 1), "unsafe path", 400},
		{"large file", packageEntry("large", "100644", "blob", "large", (32<<20)+1), "exceeds", 413},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPackageHTTPFixture(t, map[string]any{
				"/api/repos/acme/repo/git/trees/" + packageCommit:     packageTree(packageEntry("selected", "040000", "tree", "selected", 0)),
				"/api/repos/acme/repo/git/trees/selected?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 1), test.extra),
			}, nil)
			_, err := fixture.downloader().DownloadSource(context.Background(), packageSource("selected"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
			if sem := httperr.ForError(err); sem.Status != test.status {
				t.Fatalf("status=%d want%d", sem.Status, test.status)
			}
		})
	}
}

func TestGitHubDirectoryDownloadChecksRealStreamLength(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 1)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": strings.Repeat("x", (32<<20)+1)})
	_, err := fixture.downloader().DownloadSource(context.Background(), packageSource(""))
	if err == nil {
		t.Fatal("expected bounded raw stream error")
	}
	if sem := httperr.ForError(err); sem.Status != 413 {
		t.Fatalf("error=%+v", sem)
	}
}

func TestGitHubDirectoryDownloadFallsBackOnUnavailableTree(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": http.StatusForbidden,
	}, map[string]string{"/archive.zip": "archive bytes"})
	source := packageSource("")
	source.URL = fixture.server.URL + "/archive.zip"
	// A transport maps the locked GitHub archive URL to the local fixture while
	// preserving its canonical source metadata for GitHub resolution.
	source.URL = "https://github.com/acme/repo/archive/" + packageCommit + ".zip"
	downloader := fixture.downloader()
	downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
	downloaded, err := downloader.DownloadSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if downloaded.Cleanup != nil {
		defer downloaded.Cleanup()
	}
	if downloaded.PathPrefix != nil {
		t.Fatal("fallback changed source path prefix")
	}
	data, err := os.ReadFile(downloaded.Path)
	if err != nil || string(data) != "archive bytes" {
		t.Fatalf("fallback bytes=%q err=%v", data, err)
	}
}

type packageRewriteTransport struct {
	base    http.RoundTripper
	baseURL string
}

func (t packageRewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "github.com" {
		clone := r.Clone(r.Context())
		parsed, err := http.NewRequestWithContext(r.Context(), r.Method, t.baseURL+"/archive.zip", nil)
		if err != nil {
			return nil, err
		}
		clone.URL = parsed.URL
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(r)
}

func TestGitHubDirectoryDownloadDoesNotFallbackAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := HTTPZipDownloader{}.DownloadSource(ctx, packageSource(""))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestGitHubDirectoryDownloadEnforcesFileCountBeforeRawRequests(t *testing.T) {
	entries := []map[string]any{packageEntry("SKILL.md", "100644", "blob", "skill", 1)}
	for i := 0; i < 512; i++ {
		entries = append(entries, packageEntry(fmt.Sprintf("file-%03d", i), "100644", "blob", "blob", 1))
	}
	fixture := newPackageHTTPFixture(t, map[string]any{"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(entries...)}, nil)
	_, err := fixture.downloader().DownloadSource(context.Background(), packageSource(""))
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("error=%v", err)
	}
}

func TestGitHubDirectoryDownloadResolvesBranchToPinnedRawRevision(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/commits/main":                                map[string]any{"sha": packageCommit},
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 5)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "skill"})
	source := packageSource("")
	source.URL = "https://github.com/acme/repo/archive/refs/heads/main.zip"
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got := packageZipContents(t, downloaded); got["SKILL.md"] != "skill" {
		t.Fatalf("zip=%v", got)
	}
}

func TestGitHubDirectoryDownloadIsDeterministic(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("z.txt", "100644", "blob", "z", 1), packageEntry("SKILL.md", "100644", "blob", "skill", 1), packageEntry("a.txt", "100644", "blob", "a", 1)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "s", "/raw/acme/repo/" + packageCommit + "/a.txt": "a", "/raw/acme/repo/" + packageCommit + "/z.txt": "z"})
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), packageSource(""))
	if err != nil {
		t.Fatal(err)
	}
	defer downloaded.Cleanup()
	archive, err := zip.OpenReader(downloaded.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("archive order=%v", names)
	}
}

func TestGitHubDownloadFallbackSharesTransferBudget(t *testing.T) {
	stream := func(size int64) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(size))
			_, _ = io.CopyN(w, packageZeroReader{}, size)
		}
	}
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 32<<20), packageEntry("z.txt", "100644", "blob", "missing", 1)),
		"/raw/acme/repo/" + packageCommit + "/SKILL.md":                    stream(32 << 20),
		"/raw/acme/repo/" + packageCommit + "/z.txt":                       http.StatusNotFound,
		"/archive.zip": stream(70 << 20),
	}, nil)
	downloader := fixture.downloader()
	downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
	downloaded, err := downloader.DownloadSource(context.Background(), packageSource(""))
	if downloaded.Cleanup != nil {
		defer downloaded.Cleanup()
	}
	if err == nil {
		t.Fatal("expected combined directory/fallback transfer size error")
	}
	if sem := httperr.ForError(err); sem.Status != 413 {
		t.Fatalf("error=%+v", sem)
	}
}

func TestGitHubDirectoryDownloadRejectsAbsolutePathPrefix(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit:     packageTree(packageEntry("selected", "040000", "tree", "selected", 0)),
		"/api/repos/acme/repo/git/trees/selected?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 1)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/selected/SKILL.md": "s"})
	source := packageSource("selected")
	source.PathPrefix = "/selected"
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), source)
	if downloaded.Cleanup != nil {
		defer downloaded.Cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("error=%v", err)
	}
}

func TestGitHubDirectoryDownloadHonorsResolvedEmptyPrefixForSlashBranchRoot(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 1)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "s"})
	source := packageSource("")
	source.SourceURL = "https://github.com/acme/repo/tree/feature/foo"
	source.PathPrefixResolved = true
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got := packageZipContents(t, downloaded); !reflect.DeepEqual(got, map[string]string{"SKILL.md": "s"}) {
		t.Fatalf("zip contents=%v", got)
	}
}

func TestGitHubDirectoryDownloadFallsBackOnTruncatedTree(t *testing.T) {
	tree := packageTree(packageEntry("only-known/SKILL.md", "100644", "blob", "skill", 1))
	tree["truncated"] = true
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": tree,
	}, map[string]string{"/archive.zip": "archive bytes"})
	downloader := fixture.downloader()
	downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
	downloaded, err := downloader.DownloadSource(context.Background(), packageSource(""))
	if err != nil {
		t.Fatal(err)
	}
	defer downloaded.Cleanup()
	data, err := os.ReadFile(downloaded.Path)
	if err != nil || string(data) != "archive bytes" {
		t.Fatalf("fallback bytes=%q err=%v", data, err)
	}
}

func TestGitHubDirectoryDownloadRequiresSkillAtSelectedRoot(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit:     packageTree(packageEntry("selected", "040000", "tree", "selected", 0)),
		"/api/repos/acme/repo/git/trees/selected?recursive=1": packageTree(packageEntry("child/SKILL.md", "100644", "blob", "skill", 1)),
	}, nil)
	_, err := fixture.downloader().DownloadSource(context.Background(), packageSource("selected"))
	if err == nil {
		t.Fatal("expected missing root skill error")
	}
	if sem := httperr.ForError(err); sem.Code != httperr.CodeSkillMDNotFound {
		t.Fatalf("error=%+v", sem)
	}
}

func TestGitHubDirectoryDownloadBoundsActualCombinedRawStreams(t *testing.T) {
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.CopyN(w, packageZeroReader{}, 26<<20) })
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 0), packageEntry("a.bin", "100644", "blob", "a", 0), packageEntry("b.bin", "100644", "blob", "b", 0), packageEntry("c.bin", "100644", "blob", "c", 0)),
		"/raw/acme/repo/" + packageCommit + "/SKILL.md":                    stream,
		"/raw/acme/repo/" + packageCommit + "/a.bin":                       stream,
		"/raw/acme/repo/" + packageCommit + "/b.bin":                       stream,
		"/raw/acme/repo/" + packageCommit + "/c.bin":                       stream,
	}, nil)
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), packageSource(""))
	if downloaded.Cleanup != nil {
		defer downloaded.Cleanup()
	}
	if err == nil {
		t.Fatal("expected total raw transfer error")
	}
	if sem := httperr.ForError(err); sem.Status != 413 {
		t.Fatalf("error=%+v", sem)
	}
}

func TestGitHubDirectoryDownloadFallsBackAfterAPINetworkTimeout(t *testing.T) {
	fixture := newPackageHTTPFixture(t, nil, map[string]string{"/archive.zip": "archive bytes"})
	downloader := fixture.downloader()
	downloader.client = &http.Client{Transport: packageTimeoutTransport{fallback: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}}
	downloaded, err := downloader.DownloadSource(context.Background(), packageSource(""))
	if err != nil {
		t.Fatal(err)
	}
	defer downloaded.Cleanup()
	data, err := os.ReadFile(downloaded.Path)
	if err != nil || string(data) != "archive bytes" {
		t.Fatalf("fallback bytes=%q err=%v", data, err)
	}
}

type packageTimeoutTransport struct{ fallback http.RoundTripper }

func (t packageTimeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return nil, context.DeadlineExceeded
	}
	return t.fallback.RoundTrip(r)
}

func TestGitHubExplicitRootSkillIncludesNestedSkillResources(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 1), packageEntry("examples/SKILL.md", "100644", "blob", "example", 1)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "s", "/raw/acme/repo/" + packageCommit + "/examples/SKILL.md": "e"})
	source := packageSource("")
	source.SourceURL = "https://github.com/acme/repo/blob/main/SKILL.md"
	source.PathPrefixResolved = true
	downloaded, err := fixture.downloader().DownloadSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got := packageZipContents(t, downloaded); !reflect.DeepEqual(got, map[string]string{"SKILL.md": "s", "examples/SKILL.md": "e"}) {
		t.Fatalf("zip contents=%v", got)
	}
}
