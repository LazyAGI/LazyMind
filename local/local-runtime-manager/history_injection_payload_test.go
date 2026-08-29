package main

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHistoryInjectionPayloadArchive(t *testing.T, archivePath string, entries map[string]string) string {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	digest, err := historyInjectionPayloadSHA256(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestPrepareBundledHistoryInjectionExtractsOnceIntoUserData(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "resources", "history-injection.zip")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := writeHistoryInjectionPayloadArchive(t, archivePath, map[string]string{
		"README.txt":                                      "outer metadata",
		"history-injection/README.md":                     "rules",
		"history-injection/ppt/ppt-sample-v1.zip":         "ppt",
		"history-injection/product/product-sample-v1.zip": "product",
	})
	targetRoot := filepath.Join(root, "user-data", "history-injection")
	paths := RuntimePaths{
		HistoryInjectionArchive: archivePath,
		HistoryInjectionSHA256:  digest,
		HistoryInjectionRoot:    targetRoot,
	}
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	pptPath := filepath.Join(targetRoot, "ppt", "ppt-sample-v1.zip")
	if body, err := os.ReadFile(pptPath); err != nil || string(body) != "ppt" {
		t.Fatalf("extracted PPT bundle = %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "README.md")); err != nil {
		t.Fatalf("injection README missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "README.txt")); !os.IsNotExist(err) {
		t.Fatalf("outer metadata must not enter discovery root: %v", err)
	}
	if err := os.WriteFile(pptPath, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(pptPath); err != nil || string(body) != "kept" {
		t.Fatalf("matching marker should skip extraction; got %q, %v", body, err)
	}
}

func TestPrepareBundledHistoryInjectionReplacesPreviousPackage(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "history-injection.zip")
	targetRoot := filepath.Join(root, "data", "history-injection")
	digest := writeHistoryInjectionPayloadArchive(t, archivePath, map[string]string{
		"history-injection/ppt/old.zip": "old",
	})
	paths := RuntimePaths{HistoryInjectionArchive: archivePath, HistoryInjectionSHA256: digest, HistoryInjectionRoot: targetRoot}
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	digest = writeHistoryInjectionPayloadArchive(t, archivePath, map[string]string{
		"history-injection/image/new.zip": "new",
	})
	paths.HistoryInjectionSHA256 = digest
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "ppt", "old.zip")); !os.IsNotExist(err) {
		t.Fatalf("old package survived replacement: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(targetRoot, "image", "new.zip")); err != nil || string(body) != "new" {
		t.Fatalf("new package = %q, %v", body, err)
	}
}

func TestPrepareBundledHistoryInjectionRejectsUnsafePath(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "history-injection.zip")
	digest := writeHistoryInjectionPayloadArchive(t, archivePath, map[string]string{
		"history-injection/ppt/ok.zip": "ok",
		"../outside.txt":               "unsafe",
	})
	err := prepareBundledHistoryInjection(context.Background(), RuntimePaths{
		HistoryInjectionArchive: archivePath,
		HistoryInjectionSHA256:  digest,
		HistoryInjectionRoot:    filepath.Join(root, "data", "history-injection"),
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("prepare error = %v, want unsafe path", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "outside.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe payload escaped: %v", statErr)
	}
}

func TestPrepareBundledHistoryInjectionRejectsChecksumMismatchWithoutReplacingData(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "history-injection.zip")
	writeHistoryInjectionPayloadArchive(t, archivePath, map[string]string{
		"history-injection/ppt/new.zip": "new",
	})
	targetRoot := filepath.Join(root, "data", "history-injection")
	if err := os.MkdirAll(filepath.Join(targetRoot, "ppt"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(targetRoot, "ppt", "old.zip")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := prepareBundledHistoryInjection(context.Background(), RuntimePaths{
		HistoryInjectionArchive: archivePath,
		HistoryInjectionSHA256:  strings.Repeat("0", 64),
		HistoryInjectionRoot:    targetRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("prepare error = %v, want checksum mismatch", err)
	}
	if body, readErr := os.ReadFile(oldPath); readErr != nil || string(body) != "old" {
		t.Fatalf("existing data was modified: %q, %v", body, readErr)
	}
}

func TestPrepareBundledHistoryInjectionWithoutManifestArchiveIsNoOp(t *testing.T) {
	if err := prepareBundledHistoryInjection(context.Background(), RuntimePaths{}); err != nil {
		t.Fatal(err)
	}
}
