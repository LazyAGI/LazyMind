package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestApplyDesktopManifestPathsLoadsTrustedLocalMode(t *testing.T) {
	resourcesRoot := t.TempDir()
	manifest := RuntimeManifest{
		Version:  1,
		Profile:  "desktop",
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
		Features: RuntimeManifestFeatures{TrustedLocalMode: true},
		Paths: RuntimeManifestPaths{
			HistoryInjectionArchive: "history-injection.zip",
		},
		Checksums: map[string]string{
			"history-injection.zip": "abcdef",
		},
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal runtime manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resourcesRoot, runtimeManifestFileName), body, 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
	paths := RuntimePaths{ResourcesRoot: resourcesRoot}

	if err := applyDesktopManifestPaths(&paths); err != nil {
		t.Fatalf("apply desktop runtime manifest: %v", err)
	}

	if !paths.TrustedLocalMode {
		t.Fatal("trusted local mode was not loaded from the desktop runtime manifest")
	}
	if paths.HistoryInjectionArchive != filepath.Join(resourcesRoot, "history-injection.zip") {
		t.Fatalf("history injection archive = %q", paths.HistoryInjectionArchive)
	}
	if paths.HistoryInjectionSHA256 != "abcdef" {
		t.Fatalf("history injection checksum = %q", paths.HistoryInjectionSHA256)
	}
	if paths.HistoryInjectionRoot != filepath.Join(paths.DataDir, "history-injection") {
		t.Fatalf("history injection root = %q", paths.HistoryInjectionRoot)
	}
}
