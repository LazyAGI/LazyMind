package systemdeps

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
	"runtime"
	"strings"
	"testing"
)

func testPythonEntry() PythonComponent {
	return PythonComponent{ID: "rag", Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64),
		BaseFingerprint: strings.Repeat("c", 64), Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: "cp311", SizeBytes: 4, UnpackedBytes: 4}
}

func writePythonZIP(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range entries {
		item, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = item.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestPythonComponentExtractRejectsTraversalAndOversize(t *testing.T) {
	for _, name := range []string{"site-packages/../../escape", "site-packages/..\\..\\escape", "other.py"} {
		path := writePythonZIP(t, map[string]string{name: "bad"})
		if err := extractPythonComponent(context.Background(), path, t.TempDir(), 3); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	path := writePythonZIP(t, map[string]string{"site-packages/module.py": strings.Repeat("x", 2*1024*1024)})
	if err := extractPythonComponent(context.Background(), path, t.TempDir(), 1); err == nil {
		t.Fatal("accepted oversized archive")
	}
}

func TestPythonComponentActivationRequiresCompatibleManifestAndReadyMarker(t *testing.T) {
	root := t.TempDir()
	entry := testPythonEntry()
	dir := pythonComponentDir(root, entry)
	if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(entry)
	os.WriteFile(filepath.Join(dir, "bundle-manifest.json"), data, 0644)
	if pythonComponentInstalled(root, entry) {
		t.Fatal("accepted partial installation")
	}
	os.WriteFile(filepath.Join(dir, ".ready"), []byte(entry.SHA256), 0644)
	if !pythonComponentInstalled(root, entry) {
		t.Fatal("rejected completed installation")
	}
	t.Setenv("LAZYMIND_RAG_ENABLED", "0")
	state := pythonComponentStatus(root, &pythonCatalog{Components: map[string]PythonComponent{"rag": entry}}, "rag")
	if !state.Installed || state.Active || !state.RestartRequired {
		t.Fatalf("bad activation status: %+v", state)
	}
	entry.PythonABI = "cp312"
	if pythonComponentInstalled(root, entry) {
		t.Fatal("accepted incompatible ABI")
	}
}

func TestPythonComponentDownloadVerifiesSizeAndHash(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("test")) }))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	entry := testPythonEntry()
	sum := sha256.Sum256([]byte("test"))
	entry.SHA256 = hex.EncodeToString(sum[:])
	path := filepath.Join(t.TempDir(), "download.zip")
	if err := downloadPythonComponent(context.Background(), entry, server.URL, path); err != nil {
		t.Fatal(err)
	}
	entry.SHA256 = strings.Repeat("0", 64)
	if err := downloadPythonComponent(context.Background(), entry, server.URL, path); err == nil {
		t.Fatal("accepted corrupt archive")
	}
	entry.SizeBytes = 3
	if err := downloadPythonComponent(context.Background(), entry, server.URL, path); err == nil {
		t.Fatal("accepted wrong size")
	}
	if err := downloadPythonComponent(context.Background(), entry, "http://localhost/file", path); err == nil {
		t.Fatal("accepted HTTP")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := downloadPythonComponent(ctx, entry, server.URL, path); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestPythonComponentMissingRAGRespondsWithInstallLink(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "0")
	called := false
	handler := RequireRAG(func(w http.ResponseWriter, r *http.Request) { called = true })
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest("POST", "/", nil))
	if called || response.Code != 503 || !strings.Contains(response.Body.String(), "CAPABILITY_REQUIRED") {
		t.Fatalf("unexpected response: %s", response.Body)
	}
	t.Setenv("LAZYMIND_RAG_ENABLED", "1")
	handler(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
	if !called {
		t.Fatal("installed RAG blocked")
	}
}

// Set these only for an opt-in test against real, platform-matched build artifacts.
// It uses the production download/extract/import/atomic-activation path, with a
// local TLS server instead of publishing anything to a cloud account.
func TestPythonComponentRealBundles(t *testing.T) {
	catalogPath := os.Getenv("LAZYMIND_TEST_COMPONENT_CATALOG")
	if catalogPath == "" {
		t.Skip("no real component artifacts provided")
	}
	t.Setenv("LAZYMIND_PYTHON_COMPONENT_CATALOG", catalogPath)
	catalog, err := loadPythonCatalog()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	server := httptest.NewTLSServer(http.FileServer(http.Dir(filepath.Dir(catalogPath))))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	for _, id := range []string{"rag"} {
		t.Run(id, func(t *testing.T) {
			entry := catalog.Components[id]
			if err := installPythonComponent(context.Background(), root, entry, server.URL+"/"+entry.Filename); err != nil {
				t.Fatal(err)
			}
			if !pythonComponentInstalled(root, entry) {
				t.Fatal("missing completed installation")
			}
			// Reinstall is idempotent and needs no second download.
			if err := installPythonComponent(context.Background(), root, entry, "invalid"); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Reject a wrong archive without removing an existing component version.
	entry := catalog.Components["rag"]
	old := pythonComponentDir(root, entry)
	entry.Revision = strings.Repeat("d", 64)
	if err := installPythonComponent(context.Background(), root, entry, server.URL+"/"+entry.Filename); err == nil {
		t.Fatal("accepted wrong revision")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("removed previous installation")
	}
}

func TestPythonComponentInstallAPIRejectsCloudAndUnknownIDs(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
	response := httptest.NewRecorder()
	InstallPythonComponent(response, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"id":"rag"}`)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("got %d", response.Code)
	}
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	response = httptest.NewRecorder()
	InstallPythonComponent(response, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"id":"../bad"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d", response.Code)
	}
}

func TestPythonComponentInstallAPIRejectsCustomURL(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	response := httptest.NewRecorder()
	InstallPythonComponent(response, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"id":"rag","url":"https://other.example/component.zip"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("custom URL accepted: %d", response.Code)
	}
}

func TestPythonComponentInstallAPIUsesCatalogSource(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_RUNTIME_ROOT", t.TempDir())
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = r.URL.Path == "/published.zip"
		// Invalid archive: verify source selection without launching Python.
		_, _ = w.Write([]byte("test"))
	}))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	entry := testPythonEntry()
	entry.URL = server.URL + "/published.zip"
	catalog := pythonCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]PythonComponent{"rag": entry}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_PYTHON_COMPONENT_CATALOG", path)
	response := httptest.NewRecorder()
	InstallPythonComponent(response, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"id":"rag"}`)))
	if !called {
		t.Fatal("install did not download the catalog URL")
	}
	if response.Code != http.StatusBadRequest {
		t.Fatalf("bad checksum accepted: %d", response.Code)
	}
}
