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
