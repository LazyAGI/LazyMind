package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/skillv2/httperr"
)

// Serializing file HTTP requests or losing the underlying client's cache
// identity makes repeat imports slower while returning the same package.
func TestGitHubDownloadFetchesFilesConcurrentlyAndReusesTree(t *testing.T) {
	const files = 16
	var active, maxActive, trees atomic.Int32
	entries := make([]map[string]any, 0, files)
	for i := 0; i < files; i++ {
		name := fmt.Sprintf("file-%02d.txt", i)
		if i == 0 {
			name = "SKILL.md"
		}
		entries = append(entries, packageEntry(name, "100644", "blob", fmt.Sprint(i), 1))
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); now > old; old = maxActive.Load() {
			if maxActive.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = io.WriteString(w, "x")
	})
	routes := map[string]any{"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trees.Add(1)
		_ = json.NewEncoder(w).Encode(packageTree(entries...))
	})}
	for _, entry := range entries {
		routes["/raw/acme/repo/"+packageCommit+"/"+entry["path"].(string)] = handler
	}
	fixture := newPackageHTTPFixture(t, routes, nil)
	downloader := fixture.downloader()
	start := time.Now()
	for range 2 {
		got, err := downloader.DownloadSource(context.Background(), packageSource(""))
		if err != nil {
			t.Fatal(err)
		}
		if len(packageZipContents(t, got)) != files {
			t.Fatal("missing files")
		}
	}
	t.Logf("2x16 files @30ms: elapsed=%s peak=%d metadata=%d", time.Since(start), maxActive.Load(), trees.Load())
	if maxActive.Load() < 2 || maxActive.Load() > 8 {
		t.Errorf("raw fetch concurrency=%d, want bounded parallel fetching", maxActive.Load())
	}
	if trees.Load() != 1 {
		t.Errorf("tree fetched %d times, want cached immutable tree", trees.Load())
	}
}

// Successful but incomplete raw responses must not be persisted as a package;
// an independently retrieved pinned archive can repair this retrieval failure.
func TestGitHubDownloadRepairsRawSizeMismatchWithArchive(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 100)),
	}, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "short", "/archive.zip": "verified archive"})
	downloader := fixture.downloader()
	downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
	got, err := downloader.DownloadSource(context.Background(), packageSource(""))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Cleanup()
	body, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "verified archive" {
		t.Fatalf("persisted incomplete raw package; got %d bytes", len(body))
	}
}

func TestGitHubParallelDownloadCancellationCleansTemporaryFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	started := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			_ = json.NewEncoder(w).Encode(packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 1), packageEntry("a.txt", "100644", "blob", "a", 1)))
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	downloader := HTTPZipDownloader{client: server.Client(), githubAPIBaseURL: server.URL + "/api", githubRawBaseURL: server.URL + "/raw"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := downloader.DownloadSource(ctx, packageSource("")); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("raw request never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop file workers")
	}
	entries, err := os.ReadDir(os.TempDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary artifacts=%v err=%v", entries, err)
	}
}

type importFixtureTransport func(*http.Request) (*http.Response, error)

func (f importFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSkillsSHImportReusesDiscoveredArchivePackage(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{"repo-root/catalog/actual/SKILL.md": "---\nname: wanted\ndescription: wanted skill\n---\n# Wanted", "repo-root/catalog/actual/data.bin": "\x00\xff\x01", "repo-root/other/SKILL.md": "---\nname: other\ndescription: other\n---\n"} {
		entry, _ := writer.Create(name)
		_, _ = io.WriteString(entry, body)
	}
	_ = writer.Close()
	var archives atomic.Int32
	transport := importFixtureTransport(func(r *http.Request) (*http.Response, error) {
		body := []byte("<html></html>")
		code := 200
		headers := make(http.Header)
		switch r.URL.Hostname() {
		case "api.github.com":
			code = 403
			headers.Set("X-RateLimit-Remaining", "0")
			body = []byte(`{"message":"API rate limit exceeded"}`)
		case "github.com":
			if strings.HasSuffix(r.URL.Path, ".git/info/refs") {
				line := packageCommit + " HEAD\x00symref=HEAD:refs/heads/main\n"
				body = []byte(fmt.Sprintf("%04x%s0000", len(line)+4, line))
			} else {
				archives.Add(1)
				body = buffer.Bytes()
			}
		case "codeload.github.com":
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) < time.Minute {
				t.Errorf("repository archive inherited short metadata timeout: %v", deadline)
			}
			archives.Add(1)
			body = buffer.Bytes()
		}
		return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original })
	source, err := ResolveURLSource(context.Background(), "https://skills.sh/acme/repo/wanted")
	if err != nil {
		t.Fatal(err)
	}
	downloader := HTTPZipDownloader{client: &http.Client{Transport: transport}}
	got, err := downloader.DownloadSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	contents := packageZipContents(t, got)
	if archives.Load() != 1 || contents["data.bin"] != "\x00\xff\x01" || !strings.Contains(contents["SKILL.md"], "# Wanted") || len(contents) != 2 {
		t.Fatalf("archive downloads=%d files=%v", archives.Load(), contents)
	}
}

func TestGitHubDownloadIncludesResolutionBytesInTransferBudget(t *testing.T) {
	fixture := newPackageHTTPFixture(t, map[string]any{"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 1))}, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "s"})
	source := packageSource("")
	source.ResolutionTransferBytes = MaxSkillDownloadBytes - 1
	got, err := fixture.downloader().DownloadSource(context.Background(), source)
	if got.Cleanup != nil {
		got.Cleanup()
	}
	if err == nil || httperr.ForError(err).Status != 413 {
		t.Fatalf("resolution bytes were not counted against download: %v", err)
	}
	if len(fixture.requests) != 1 {
		t.Fatalf("oversized transfer retried through fallback: %v", fixture.requests)
	}
}
