package systemdeps

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallBrowserExtensionFromDesktopSource(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv(browserExtensionBundlePathEnv, "")
	t.Setenv(browserExtensionBundleURLEnv, "")
	t.Setenv(browserExtensionBundleSHAEnv, "")
	source := createBrowserExtensionSource(t, t.TempDir(), "0.2.0")
	t.Setenv(browserExtensionSourceDirEnv, source)
	runtimeRoot := t.TempDir()

	status, err := InstallBrowserExtension(context.Background(), runtimeRoot)
	if err != nil {
		t.Fatalf("install browser extension: %v", err)
	}
	if !status.Installed || status.Version != "0.2.0" {
		t.Fatalf("unexpected installed status: %#v", status)
	}
	if status.AvailableVersion != "0.2.0" || status.UpdateAvailable {
		t.Fatalf("unexpected source version status: %#v", status)
	}
	if !status.BrowserApprovalRequired || status.BrowserSettingsURL != "chrome://extensions" {
		t.Fatalf("browser approval boundary missing: %#v", status)
	}
	if len(status.SupportedBrowsers) != 2 ||
		status.SupportedBrowsers[0].ID != "chrome" || status.SupportedBrowsers[0].SettingsURL != "chrome://extensions" ||
		status.SupportedBrowsers[1].ID != "edge" || status.SupportedBrowsers[1].SettingsURL != "edge://extensions" {
		t.Fatalf("Chrome/Edge install targets missing: %#v", status.SupportedBrowsers)
	}
	if status.InstallDir != BrowserExtensionInstallDir(runtimeRoot) {
		t.Fatalf("install dir = %q", status.InstallDir)
	}
	if _, err := os.Stat(filepath.Join(status.InstallDir, "src", "background.js")); err != nil {
		t.Fatalf("installed service worker: %v", err)
	}
	cfg, err := LoadConfig(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BrowserExtension.InstalledDir != status.InstallDir {
		t.Fatalf("saved install dir = %q", cfg.BrowserExtension.InstalledDir)
	}
	createBrowserExtensionSource(t, source, "0.3.0")
	status, err = DetectBrowserExtension(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !status.UpdateAvailable || status.AvailableVersion != "0.3.0" || status.Version != "0.2.0" {
		t.Fatalf("browser extension update was not detected: %#v", status)
	}
}

func TestInstallBrowserExtensionDownloadsVerifiedNestedBundle(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv(browserExtensionBundlePathEnv, "")
	t.Setenv(browserExtensionSourceDirEnv, "")
	bundle := browserExtensionZIP(t, "browser-extension/", "0.3.0")
	checksum := fmt.Sprintf("%x", sha256.Sum256(bundle))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bundle)
	}))
	defer server.Close()
	t.Setenv(browserExtensionBundleURLEnv, server.URL+"/browser-extension.zip")
	t.Setenv(browserExtensionBundleSHAEnv, checksum)

	status, err := InstallBrowserExtension(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("download browser extension: %v", err)
	}
	if !status.Installed || status.Version != "0.3.0" {
		t.Fatalf("unexpected downloaded status: %#v", status)
	}
}

func TestInstallBrowserExtensionRejectsChecksumMismatch(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv(browserExtensionBundlePathEnv, "")
	t.Setenv(browserExtensionSourceDirEnv, "")
	bundle := browserExtensionZIP(t, "", "0.3.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bundle)
	}))
	defer server.Close()
	t.Setenv(browserExtensionBundleURLEnv, server.URL)
	t.Setenv(browserExtensionBundleSHAEnv, strings.Repeat("0", 64))
	runtimeRoot := t.TempDir()

	if _, err := InstallBrowserExtension(context.Background(), runtimeRoot); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	if _, err := os.Stat(BrowserExtensionInstallDir(runtimeRoot)); !os.IsNotExist(err) {
		t.Fatalf("invalid bundle should not be activated: %v", err)
	}
}

func TestInstallBrowserExtensionRejectsUnsafeArchivePath(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv(browserExtensionBundleURLEnv, "")
	t.Setenv(browserExtensionBundleSHAEnv, "")
	t.Setenv(browserExtensionSourceDirEnv, "")
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	entry, err := writer.Create("../outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("unsafe"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "unsafe.zip")
	if err := os.WriteFile(archivePath, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(browserExtensionBundlePathEnv, archivePath)

	if _, err := InstallBrowserExtension(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("expected unsafe archive rejection, got %v", err)
	}
}

func createBrowserExtensionSource(t *testing.T, root, version string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"manifest_version":3,"name":"LazyMind Browser","version":%q,"background":{"service_worker":"src/background.js"},"action":{"default_popup":"src/popup.html"}}`, version)
	for relative, content := range map[string]string{
		"manifest.json":     manifest,
		"src/background.js": "void 0;",
		"src/popup.html":    "<!doctype html>",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func browserExtensionZIP(t *testing.T, prefix, version string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	manifest := fmt.Sprintf(`{"manifest_version":3,"name":"LazyMind Browser","version":%q,"background":{"service_worker":"src/background.js"},"action":{"default_popup":"src/popup.html"}}`, version)
	for relative, content := range map[string]string{
		"manifest.json":     manifest,
		"src/background.js": "void 0;",
		"src/popup.html":    "<!doctype html>",
	} {
		entry, err := writer.Create(prefix + relative)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
