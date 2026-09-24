package showcase

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testDigest(data []byte) string { d := sha256.Sum256(data); return hex.EncodeToString(d[:]) }

func TestDeferredAssetDownloadCacheAndUnknownPaths(t *testing.T) {
	name := "demo/1/result.html"
	payload := []byte("<html>verified preview</html>")
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, _ := zw.Create(name)
	f.Write(payload)
	zw.Close()
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/ms/case.zip" {
			http.NotFound(w, r)
			return
		}
		w.Write(archive.Bytes())
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	featured := t.TempDir()
	cache := t.TempDir()
	desc := assetDownloads{SchemaVersion: 1, Bundles: map[string]assetBundle{"demo/1": {
		Filename: "case.zip", URL: server.URL + "/ms/case.zip", FallbackURL: server.URL + "/hf/case.zip",
		SHA256: testDigest(archive.Bytes()), Size: int64(archive.Len()), Files: map[string]assetFile{name: {testDigest(payload), int64(len(payload))}},
	}}}
	data, _ := json.Marshal(desc)
	os.WriteFile(filepath.Join(featured, "downloads.json"), data, 0600)
	request := func(name string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		serveAssetFrom(w, httptest.NewRequest("GET", "/showcase-assets/"+name, nil), featured, cache)
		return w
	}
	if w := request("demo/1/unknown.html"); w.Code != 404 || hits.Load() != 0 {
		t.Fatalf("unknown path fetched: %d", w.Code)
	}
	for i := 0; i < 2; i++ {
		w := request(name)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), payload) {
			t.Fatalf("response: %d %s", w.Code, w.Body)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("cache not reused: %d", hits.Load())
	}
	// A corrupt extracted asset is detected and replaced with a verified copy.
	target := filepath.Join(cache, testDigest(archive.Bytes()), filepath.FromSlash(name))
	os.WriteFile(target, []byte("bad"), 0600)
	if w := request(name); w.Code != 200 || hits.Load() != 4 {
		t.Fatalf("repair failed: %d hits=%d", w.Code, hits.Load())
	}
}

func TestAssetExtractionRejectsUnsafeIncompleteAndCorruptArchives(t *testing.T) {
	for _, name := range []string{"../escape", "demo/1/other.html", "demo/1/result.html"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			f, _ := z.Create(name)
			f.Write([]byte("bad"))
			z.Close()
			root := t.TempDir()
			archive := filepath.Join(root, "case.zip")
			os.WriteFile(archive, b.Bytes(), 0600)
			expected := map[string]assetFile{"demo/1/result.html": {testDigest([]byte("yes")), 3}}
			if err := extractAssets(archive, filepath.Join(root, "out"), expected); err == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
}

func TestPublishedFeaturedArtifacts(t *testing.T) {
	root := os.Getenv("LAZYMIND_TEST_FEATURED_RUNTIME")
	if root == "" {
		t.Skip("no staged featured artifacts")
	}
	featured := filepath.Join(root, "featured-skills")
	catalog, err := LoadCatalog(filepath.Join(featured, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var desc assetDownloads
	body, err := os.ReadFile(filepath.Join(featured, "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(body, &desc); err != nil {
		t.Fatal(err)
	}
	for _, c := range catalog.Cases {
		for _, asset := range c.Assets {
			name := asset.URL[len("/showcase-assets/"):]
			if file, ok := desc.Local[name]; ok {
				if !assetMatches(filepath.Join(featured, "assets", filepath.FromSlash(name)), file) {
					t.Fatal(name)
				}
			} else {
				entry := desc.Bundles[c.ID+"/"+c.Version]
				if _, ok := entry.Files[name]; !ok {
					t.Fatal("missing remote asset", name)
				}
			}
		}
	}
	zipRoot := os.Getenv("LAZYMIND_TEST_FEATURED_ZIPS")
	for _, entry := range desc.Bundles {
		archive := filepath.Join(zipRoot, entry.Filename)
		if !assetMatches(archive, assetFile{entry.SHA256, entry.Size}) {
			t.Fatal(entry.Filename)
		}
		if err := extractAssets(archive, t.TempDir(), entry.Files); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublishedFeaturedRemoteFallback(t *testing.T) {
	descriptor := os.Getenv("LAZYMIND_TEST_FEATURED_REMOTE")
	if descriptor == "" {
		t.Skip("public resource integration is opt-in")
	}
	body, err := os.ReadFile(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	var downloads assetDownloads
	if err := json.Unmarshal(body, &downloads); err != nil {
		t.Fatal(err)
	}
	var selected assetBundle
	for _, entry := range downloads.Bundles {
		if selected.Size == 0 || entry.Size < selected.Size {
			selected = entry
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cache := t.TempDir()
	target, err := ensureAssetBundle(ctx, cache, selected)
	if err != nil {
		t.Fatal(err)
	}
	for name, entry := range selected.Files {
		if !assetMatches(filepath.Join(target, filepath.FromSlash(name)), entry) {
			t.Fatal(name)
		}
	}
	// Cached content remains usable even with both sources unavailable.
	selected.URL = "https://offline.invalid/" + selected.Filename
	selected.FallbackURL = ""
	if _, err := ensureAssetBundle(ctx, cache, selected); err != nil {
		t.Fatal(err)
	}
	t.Logf("Public bundle and offline cache verified: %s", selected.Filename)
}
