package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResetPathsDoNotIncludeLegacyRepoDataDirs(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	legacyDataRoot := filepath.Join(paths.RepoRoot, "data") + string(filepath.Separator)
	for _, path := range append(localKBResetPaths(paths), localAllResetPaths(paths)...) {
		if path == filepath.Join(paths.RepoRoot, "data") || strings.HasPrefix(path, legacyDataRoot) {
			t.Fatalf("reset path %q must not target legacy repo data dir", path)
		}
	}
}

func TestResetPathsIncludeRuntimeUploadData(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	assertStringSliceContains(t, localKBResetPaths(paths), paths.UploadRoot)
	assertStringSliceContains(t, localAllResetPaths(paths), filepath.Join(paths.RuntimeRoot, "data"))
}

func assertStringSliceContains(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if item == want {
			return
		}
	}
	t.Fatalf("missing %q in %#v", want, items)
}
