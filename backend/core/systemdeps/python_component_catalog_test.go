package systemdeps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func taggedPythonEntry() PythonComponent {
	entry := testPythonEntry()
	entry.Tag = entry.Revision
	entry.Filename = "rag.zip"
	entry.URL = "https://components.example/rag.zip"
	entry.CatalogURL = "https://components.example/tags/" + entry.Tag + "/python-components.json"
	entry.Packages = map[string]string{"orjson": "3.11.3"}
	return entry
}

func TestOnlinePythonCatalogAcceptsResignedRevision(t *testing.T) {
	pinned := taggedPythonEntry()
	updated := pinned
	updated.Revision, updated.SHA256 = strings.Repeat("d", 64), strings.Repeat("e", 64)
	updated.Filename, updated.URL = "signed.zip", "https://components.example/signed.zip"
	updated.CatalogURL = "https://must-not-replace-channel.example/catalog.json"
	updated.Tag = "" // Hand-published schema-1 catalogs need not repeat the tag.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(pythonCatalog{SchemaVersion: 1, Platform: pinned.Platform, Arch: pinned.Arch,
			Components: map[string]PythonComponent{"rag": updated}})
	}))
	defer server.Close()
	pinned.CatalogURL = server.URL
	resolved, err := resolvePythonDescriptor(context.Background(), server.Client(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Revision != updated.Revision || resolved.SHA256 != updated.SHA256 ||
		resolved.Tag != pinned.Tag || resolved.CatalogURL != pinned.CatalogURL {
		t.Fatalf("incorrect resolved descriptor: %+v", resolved)
	}
}

func TestOnlinePythonCatalogRejectsIncompatibleOrUnsafeMetadata(t *testing.T) {
	pinned := taggedPythonEntry()
	for _, field := range []string{"base", "abi", "arch", "packages", "sha", "revision", "size", "filename", "url", "tag", "schema", "catalogPlatform"} {
		t.Run(field, func(t *testing.T) {
			updated := pinned
			catalog := pythonCatalog{SchemaVersion: 1, Platform: pinned.Platform, Arch: pinned.Arch}
			switch field {
			case "base":
				updated.BaseFingerprint = strings.Repeat("f", 64)
			case "abi":
				updated.PythonABI = "cp312"
			case "arch":
				updated.Arch = "other"
			case "packages":
				updated.Packages = map[string]string{"orjson": "0.1"}
			case "sha":
				updated.SHA256 = "bad"
			case "revision":
				updated.Revision = "../escape"
			case "size":
				updated.SizeBytes = 0
			case "filename":
				updated.Filename = "../escape.zip"
			case "url":
				updated.URL = "http://components.example/rag.zip"
			case "tag":
				updated.Tag = strings.Repeat("f", 64)
			case "schema":
				catalog.SchemaVersion = 2
			case "catalogPlatform":
				catalog.Platform = "other"
			}
			catalog.Components = map[string]PythonComponent{"rag": updated}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(catalog) }))
			defer server.Close()
			pinned.CatalogURL = server.URL
			if _, err := resolvePythonDescriptor(context.Background(), server.Client(), pinned); err == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
}

func TestOnlinePythonCatalogFailsClosed(t *testing.T) {
	for _, mode := range []string{"downgrade", "oversize", "missing", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "downgrade":
					http.Redirect(w, r, "http://127.0.0.1/unsafe", http.StatusFound)
				case "oversize":
					w.Write([]byte(strings.Repeat("x", 1024*1024+1)))
				case "missing":
					http.NotFound(w, r)
				case "malformed":
					w.Write([]byte("not JSON"))
				}
			}))
			defer server.Close()
			entry := taggedPythonEntry()
			entry.CatalogURL = server.URL
			if _, err := resolvePythonDescriptor(context.Background(), server.Client(), entry); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}

func TestPythonCatalogReceiptRequiresVerifiedInstallation(t *testing.T) {
	root := t.TempDir()
	pinned := taggedPythonEntry()
	updated := pinned
	updated.Revision, updated.SHA256 = strings.Repeat("d", 64), strings.Repeat("e", 64)
	dir := pythonComponentDir(root, updated)
	if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(updated)
	if err := os.WriteFile(filepath.Join(dir, "bundle-manifest.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := savePythonDescriptor(root, updated); err != nil {
		t.Fatal(err)
	}
	if got := installedPythonDescriptor(root, pinned); got.Revision != pinned.Revision {
		t.Fatal("trusted incomplete installation")
	}
	if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(updated.SHA256), 0644); err != nil {
		t.Fatal(err)
	}
	if got := installedPythonDescriptor(root, pinned); got.Revision != updated.Revision {
		t.Fatal("did not reuse verified receipt offline")
	}
	// Replacing the receipt must remain atomic and support successive repairs.
	updated.Tag = strings.Repeat("f", 64)
	data, _ = json.Marshal(updated)
	if err := os.WriteFile(pythonDescriptorPath(root, pinned), data, 0644); err != nil {
		t.Fatal(err)
	}
	if got := installedPythonDescriptor(root, pinned); got.Revision != pinned.Revision {
		t.Fatal("accepted receipt from another tag")
	}
	updated.Tag = pinned.Tag
	if err := savePythonDescriptor(root, updated); err != nil {
		t.Fatal(err)
	}
}

func TestOnlinePythonComponentInstallUsesRemoteHashAndPersistsReceipt(t *testing.T) {
	// Synthetic archive: bypass only the Python import subprocess, while testing
	// production HTTPS resolution, checksum, extraction, activation and receipts.
	python, err := exec.LookPath("true")
	if err != nil {
		t.Skip("requires a no-op subprocess for synthetic import fixture")
	}
	t.Setenv("LAZYMIND_ALGORITHM_PYTHON", python)
	root := t.TempDir()
	t.Setenv("LAZYMIND_RUNTIME_ROOT", root)
	pinned := taggedPythonEntry()
	updated := pinned
	updated.Revision = strings.Repeat("d", 64)
	manifest, _ := json.Marshal(updated)
	archive := writePythonZIP(t, map[string]string{"bundle-manifest.json": string(manifest), "site-packages/fixture.py": "pass"})
	payload, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	updated.SHA256, updated.SizeBytes = hex.EncodeToString(sum[:]), int64(len(payload))
	var server *httptest.Server
	var corrupt atomic.Bool
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog.json" {
			entry := updated
			entry.URL = server.URL + "/rag.zip"
			json.NewEncoder(w).Encode(pythonCatalog{SchemaVersion: 1, Platform: pinned.Platform, Arch: pinned.Arch,
				Components: map[string]PythonComponent{"rag": entry}})
		} else if corrupt.Load() {
			w.Write(append([]byte{payload[0] ^ 1}, payload[1:]...))
		} else {
			w.Write(payload)
		}
	}))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	pinned.CatalogURL = server.URL + "/catalog.json"
	if err := installPythonComponent(context.Background(), root, pinned, pinned.URL); err != nil {
		t.Fatal(err)
	}
	selected := installedPythonDescriptor(root, pinned)
	if selected.Revision != updated.Revision || selected.SHA256 != updated.SHA256 || !pythonComponentInstalled(root, selected) {
		t.Fatal("online installation did not persist the verified new identity")
	}
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	data, _ := json.Marshal(pythonCatalog{SchemaVersion: 1, Platform: pinned.Platform, Arch: pinned.Arch, Components: map[string]PythonComponent{"rag": pinned}})
	if err := os.WriteFile(catalogPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_PYTHON_COMPONENT_CATALOG", catalogPath)
	loaded, err := loadPythonCatalog()
	if err != nil || loaded.Components["rag"].Revision != selected.Revision {
		t.Fatalf("restart lost descriptor: %v", err)
	}
	corrupt.Store(true)
	if err := installPythonComponent(context.Background(), t.TempDir(), pinned, pinned.URL); err == nil {
		t.Fatal("accepted ZIP differing from remote SHA")
	}
	server.Close()
	if err := installPythonComponent(context.Background(), root, selected, "invalid"); err != nil {
		t.Fatalf("offline reuse failed: %v", err)
	}
}
