package download

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// roundTripFunc turns a handler into a RoundTripper so tests need no sockets.
type roundTripFunc func(r *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func serveBytes(handler func(r *http.Request) (*http.Response, error)) {
	defaultHTTPClient = &http.Client{Transport: roundTripFunc(handler)}
}

func responseOK(body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/octet-stream"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func TestFetchDispatchGit(t *testing.T) {
	_, err := fetchOnce(context.Background(), "https://modelscope.cn/datasets/a/b.git", "", t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected git clone to fail without git/network, got nil")
	}
	if !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("expected git clone error, got %v", err)
	}
}

func TestFetchDispatchHTTP(t *testing.T) {
	serveBytes(func(r *http.Request) (*http.Response, error) {
		return responseOK([]byte("hello")), nil
	})
	dir := t.TempDir()
	files, err := fetchOnce(context.Background(), "https://example.com/doc.txt", "", dir, nil)
	if err != nil {
		t.Fatalf("fetch http: %v", err)
	}
	if len(files) != 1 || files[0].Path != "doc.txt" {
		t.Fatalf("unexpected files %+v", files)
	}
	if files[0].SHA256 == "" || files[0].Size != 5 {
		t.Fatalf("unexpected hash/size %+v", files[0])
	}
}

func TestFetchRetriesOnce(t *testing.T) {
	var attempts int32
	serveBytes(func(r *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("boom"))}, nil
		}
		return responseOK([]byte("ok")), nil
	})
	files, err := Fetch(context.Background(), "https://example.com/a.txt", "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("fetch with retry: %v", err)
	}
	if attempts != 2 || len(files) != 1 {
		t.Fatalf("attempts=%d files=%d, want 2/1", attempts, len(files))
	}
}

func TestDownloadURLFollowsRedirect(t *testing.T) {
	serveBytes(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/redirect" {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/final"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return responseOK([]byte("final")), nil
	})
	dst := filepath.Join(t.TempDir(), "out.bin")
	if _, err := downloadURL(context.Background(), "https://example.com/redirect", dst, nil); err != nil {
		t.Fatalf("download redirect: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "final" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestFetchHTTPZipExtract(t *testing.T) {
	zipPath := makeTestZip(t, map[string]string{
		"docs/a.txt": "alpha",
		"docs/b.txt": "beta",
		"readme.md":  "# hello",
	})
	b, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	serveBytes(func(r *http.Request) (*http.Response, error) {
		return responseOK(b), nil
	})
	dir := t.TempDir()
	files, err := fetchOnce(context.Background(), "https://example.com/pkg.zip", "", dir, nil)
	if err != nil {
		t.Fatalf("fetch zip: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files=%d, want 3 (%+v)", len(files), files)
	}
	for _, f := range files {
		if !isFile(filepath.Join(dir, filepath.FromSlash(f.Path))) {
			t.Fatalf("extracted file missing: %s", f.Path)
		}
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	f, _ := os.Create(zipPath)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../evil.txt")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	f.Close()
	if err := extractZip(zipPath, t.TempDir()); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestModelscopeResolveURL(t *testing.T) {
	u, _ := url.Parse("https://www.modelscope.cn/datasets/simpleai/HC3-Chinese.git")
	rule := modelscopeLFSRule{}
	got, err := rule.ResolveURL(context.Background(), u, "master", "law.jsonl", "abc", 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/resolve/master/law.jsonl"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}

	// Default revision is master when empty; paths are URL-escaped.
	got, err = rule.ResolveURL(context.Background(), u, "", "docs/a b.jsonl", "abc", 1)
	if err != nil {
		t.Fatalf("resolve default rev: %v", err)
	}
	if !strings.Contains(got, "/resolve/master/docs/a%20b.jsonl") {
		t.Fatalf("unexpected escaped path: %s", got)
	}
}

func TestLFSPointerParseAndUnknownHost(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "train.jsonl")
	_ = os.WriteFile(ptr, []byte("version https://git-lfs.github.com/spec/v1\noid sha256:97d6fdc246c0ffdd9637a1d2ad943c8fde3553fc4764859eb61eecb022dbfb62\nsize 211404\n"), 0o644)
	if !isLFSPointerFile(ptr) {
		t.Fatal("expected LFS pointer detection")
	}
	p, err := parseLFSPointerFile(ptr)
	if err != nil {
		t.Fatalf("parse pointer: %v", err)
	}
	if p.OID != "97d6fdc246c0ffdd9637a1d2ad943c8fde3553fc4764859eb61eecb022dbfb62" || p.Size != 211404 {
		t.Fatalf("unexpected pointer %+v", p)
	}

	// Unknown host must fail with a clear error (no network needed).
	repoURL, _ := url.Parse("https://example.com/datasets/a/b.git")
	err = resolveLFSPointer(context.Background(), repoURL, "master", dir, "train.jsonl")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported host error, got %v", err)
	}
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func makeTestZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
