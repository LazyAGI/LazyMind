package systemdeps

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEditablePPTBundleConfigUsesModelScopeWithConfiguredFallback(t *testing.T) {
	t.Setenv("LAZYMIND_EDITABLE_PPT_WINDOWS_X64_URL", "https://github.com/example/windows.zip")
	t.Setenv("LAZYMIND_EDITABLE_PPT_WINDOWS_X64_SHA256", "ABCDEF")

	windows := editablePPTBundleConfigFor("windows", "amd64")
	if windows.URL != modelScopeEditablePPTBaseURL+"lazymind-editable-ppt-windows-x64-1.0.0.zip" {
		t.Fatalf("windows primary URL = %q", windows.URL)
	}
	if windows.SHA256 != windowsX64EditablePPTSHA {
		t.Fatalf("windows primary SHA256 = %q", windows.SHA256)
	}
	if windows.FallbackURL != "https://github.com/example/windows.zip" {
		t.Fatalf("windows fallback URL = %q", windows.FallbackURL)
	}
	if windows.FallbackSHA256 != "abcdef" {
		t.Fatalf("windows fallback SHA256 = %q", windows.FallbackSHA256)
	}

	darwin := editablePPTBundleConfigFor("darwin", "arm64")
	if darwin.URL != modelScopeEditablePPTBaseURL+"lazymind-editable-ppt-darwin-arm64-1.0.0.zip" {
		t.Fatalf("darwin primary URL = %q", darwin.URL)
	}
	if darwin.SHA256 != darwinArm64EditablePPTSHA {
		t.Fatalf("darwin primary SHA256 = %q", darwin.SHA256)
	}
	if !darwin.Supported {
		t.Fatal("expected darwin/arm64 to be supported")
	}
	linux := editablePPTBundleConfigFor("linux", "amd64")
	if linux.URL != modelScopeEditablePPTBaseURL+"lazymind-editable-ppt-linux-x64-1.0.0.zip" {
		t.Fatalf("linux primary URL = %q", linux.URL)
	}
	if linux.SHA256 != linuxX64EditablePPTSHA || !linux.Supported {
		t.Fatalf("linux config = %#v", linux)
	}
	if editablePPTBundleConfigFor("linux", "arm64").Supported {
		t.Fatal("expected linux/arm64 to be unsupported")
	}
}

func TestEditablePPTBundleDownloadFallsBack(t *testing.T) {
	payload := []byte("editable-ppt-bundle")
	checksum := fmt.Sprintf("%x", sha256.Sum256(payload))
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer fallback.Close()

	destination := filepath.Join(t.TempDir(), "bundle.zip")
	err := acquireEditablePPTBundleFromConfig(context.Background(), destination, editablePPTBundleConfig{
		URL:            primary.URL,
		SHA256:         checksum,
		FallbackURL:    fallback.URL,
		FallbackSHA256: checksum,
		Supported:      true,
	})
	if err != nil {
		t.Fatalf("download with fallback: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded payload = %q", got)
	}
}

func TestResolveNodeExecutableFallsBackToPath(t *testing.T) {
	t.Setenv("LAZYMIND_NODE_EXECUTABLE", "")
	binDir := t.TempDir()
	nodePath := filepath.Join(binDir, "node")
	if err := os.WriteFile(nodePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if got := resolveNodeExecutable(); got != nodePath {
		t.Fatalf("resolveNodeExecutable() = %q, want %q", got, nodePath)
	}
}

func TestEditablePPTInstallPreservesRunningExporterAndLinksNewDependencies(t *testing.T) {
	t.Setenv("LAZYMIND_PPT_EXPORT_CLI", "")
	t.Setenv("LAZYMIND_WORKFLOWS_DIR", "")
	root := t.TempDir()
	install := filepath.Join(root, "editable-ppt")
	extracted := filepath.Join(root, "payload")
	for _, version := range []string{"running-version", "upgraded-version"} {
		source := filepath.Join(install, "export-runtime", version)
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "html_to_pptx.mjs"), []byte(version), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(extracted, "node_modules", "pptxgenjs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "node_modules", "pptxgenjs", "package.json"), []byte(`{"version":"new"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceEditablePPTInstall(extracted, install); err != nil {
		t.Fatal(err)
	}
	if err := EnsureEditablePPTNodeModulesLink(install); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"running-version", "upgraded-version"} {
		source := filepath.Join(install, "export-runtime", version)
		code, err := os.ReadFile(filepath.Join(source, "html_to_pptx.mjs"))
		if err != nil || string(code) != version {
			t.Fatalf("running exporter changed: %s, %v", code, err)
		}
		data, err := os.ReadFile(filepath.Join(source, "node_modules", "pptxgenjs", "package.json"))
		if err != nil || string(data) != `{"version":"new"}` {
			t.Fatalf("exporter cannot resolve installed dependencies: %s, %v", data, err)
		}
	}
}

func TestEditablePPTInstallFailureRestoresPreviousRuntime(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "editable-ppt")
	source := filepath.Join(install, "export-runtime", "running")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(source, "html_to_pptx.mjs")
	if err := os.WriteFile(marker, []byte("running"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceEditablePPTInstall(filepath.Join(root, "missing-payload"), install); err == nil {
		t.Fatal("install with missing payload must fail")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "running" {
		t.Fatalf("failed install lost previous exporter: %s, %v", data, err)
	}
}
