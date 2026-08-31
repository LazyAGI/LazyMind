package download

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
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

func TestHuggingFaceResolveURL(t *testing.T) {
	u, _ := url.Parse("https://huggingface.co/datasets/someorg/somerepo.git")
	rule := huggingfaceLFSRule{}
	got, err := rule.ResolveURL(context.Background(), u, "main", "data/train.parquet", "abc", 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "https://huggingface.co/datasets/someorg/somerepo/resolve/main/data/train.parquet"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}

	// Default revision is main when empty; paths are URL-escaped per segment.
	got, err = rule.ResolveURL(context.Background(), u, "", "docs/a b/c.json", "abc", 1)
	if err != nil {
		t.Fatalf("resolve default rev: %v", err)
	}
	if !strings.Contains(got, "/resolve/main/docs/a%20b/c.json") {
		t.Fatalf("unexpected escaped path: %s", got)
	}

	// Models shape is also supported; namespaces may contain digits/dashes.
	um, _ := url.Parse("https://huggingface.co/models/crag-mm-2025/model-name.git")
	got, err = rule.ResolveURL(context.Background(), um, "", "weights.bin", "abc", 1)
	if err != nil {
		t.Fatalf("resolve models: %v", err)
	}
	if !strings.Contains(got, "/models/crag-mm-2025/model-name/resolve/main/weights.bin") {
		t.Fatalf("unexpected models url: %s", got)
	}

	// Malformed paths must fail.
	bad, _ := url.Parse("https://huggingface.co/datasets/onlyone.git")
	if _, err := rule.ResolveURL(context.Background(), bad, "", "x.txt", "abc", 1); err == nil {
		t.Fatal("expected error for malformed repo path")
	}
}

func TestHuggingFaceResolveLFSPointer(t *testing.T) {
	content := []byte("real huggingface lfs content")
	sum := sha256.Sum256(content)
	oid := hex.EncodeToString(sum[:])

	dir := t.TempDir()
	ptrPath := filepath.Join(dir, "data", "train.parquet")
	if err := os.MkdirAll(filepath.Dir(ptrPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ptr := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, len(content))
	if err := os.WriteFile(ptrPath, []byte(ptr), 0o644); err != nil {
		t.Fatal(err)
	}

	serveBytes(func(r *http.Request) (*http.Response, error) {
		return responseOK(content), nil
	})
	repoURL, _ := url.Parse("https://huggingface.co/datasets/someorg/somerepo.git")
	if err := resolveLFSPointer(context.Background(), repoURL, "main", dir, "data/train.parquet"); err != nil {
		t.Fatalf("resolve LFS pointer: %v", err)
	}
	got, err := os.ReadFile(ptrPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q want %q", got, content)
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

func TestExtractNestedZips(t *testing.T) {
	root := t.TempDir()

	innerZip := makeTestZip(t, map[string]string{
		"docs/b.pdf": "inner-pdf",
		"flat.md":    "# inner",
	})
	innerBytes, err := os.ReadFile(innerZip)
	if err != nil {
		t.Fatal(err)
	}
	outerZip := makeTestZip(t, map[string]string{
		"docs/a.pdf":       "outer-pdf",
		"nested/inner.zip": string(innerBytes),
	})
	outerBytes, err := os.ReadFile(outerZip)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "corpus.zip"), outerBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "keep.zip"), []byte("not-a-zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractNestedZips(root); err != nil {
		t.Fatalf("extractNestedZips: %v", err)
	}

	for _, want := range []string{"docs/a.pdf", "docs/b.pdf", "flat.md"} {
		if !isFile(filepath.Join(root, want)) {
			t.Fatalf("expected extracted file %s to exist", want)
		}
	}
	if isFile(filepath.Join(root, "corpus.zip")) {
		t.Fatal("expected outer zip to be removed after extraction")
	}
	if isFile(filepath.Join(root, "nested", "inner.zip")) {
		t.Fatal("expected inner zip to be removed after extraction")
	}
	if !isFile(filepath.Join(root, ".git", "keep.zip")) {
		t.Fatal("expected zip inside .git to be left untouched")
	}
}

func TestExtractNestedZipsNoZips(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractNestedZips(root); err != nil {
		t.Fatalf("extractNestedZips: %v", err)
	}
	if !isFile(filepath.Join(root, "docs", "a.pdf")) {
		t.Fatal("unexpected file loss")
	}
}

func TestExtractZipNonUTF8EntryNames(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "pkg.zip")

	// Windows-created archives store Chinese filenames in GBK without the
	// UTF-8 flag; the official market packages (e.g. lawtext/laws) do this.
	gbk := func(s string) string {
		encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatalf("encode GBK: %v", err)
		}
		return string(encoded)
	}
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	dirHdr := &zip.FileHeader{Name: "guohui/", NonUTF8: true}
	dirHdr.SetMode(os.ModeDir | 0o755)
	if _, err := zw.CreateHeader(dirHdr); err != nil {
		t.Fatalf("create dir entry: %v", err)
	}
	fileHdr := &zip.FileHeader{Name: "guohui/" + gbk("国徽1024.png"), NonUTF8: true}
	fileHdr.SetMode(0o644)
	w, err := zw.CreateHeader(fileHdr)
	if err != nil {
		t.Fatalf("create file entry: %v", err)
	}
	if _, err := w.Write([]byte("png-bytes")); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}

	if err := extractZip(zipPath, root); err != nil {
		t.Fatalf("extract zip: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "guohui", "国徽1024.png"))
	if err != nil {
		t.Fatalf("decoded entry missing or unreadable: %v", err)
	}
	if string(got) != "png-bytes" {
		t.Fatalf("content=%q, want png-bytes", got)
	}
	entries, err := os.ReadDir(filepath.Join(root, "guohui"))
	if err != nil {
		t.Fatalf("list extracted dir: %v", err)
	}
	for _, entry := range entries {
		if !utf8.ValidString(entry.Name()) {
			t.Fatalf("non-UTF-8 entry written on disk: %q", entry.Name())
		}
	}
}
