package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePythonPayloadArchive(t *testing.T, resourcesRoot string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(filepath.Join(resourcesRoot, pythonPayloadArchiveName))
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
}

func TestPrepareBundledPythonRuntimeExtractsPayloadOnce(t *testing.T) {
	resourcesRoot := t.TempDir()
	writePythonPayloadArchive(t, resourcesRoot, map[string]string{
		"runtimes/python/cpython/python.exe":          "python",
		"deps/python/auth-service/Scripts/python.exe": "auth",
	})
	paths := RuntimePaths{ResourcesRoot: resourcesRoot}
	if err := prepareBundledPythonRuntime(paths); err != nil {
		t.Fatal(err)
	}
	pythonPath := filepath.Join(resourcesRoot, "runtimes", "python", "cpython", "python.exe")
	if body, err := os.ReadFile(pythonPath); err != nil || string(body) != "python" {
		t.Fatalf("extracted Python = %q, %v", body, err)
	}
	markerPath := filepath.Join(resourcesRoot, pythonPayloadMarkerName)
	if marker, err := os.ReadFile(markerPath); err != nil || strings.TrimSpace(string(marker)) == "" {
		t.Fatalf("payload marker = %q, %v", marker, err)
	}
	if _, err := os.Stat(filepath.Join(resourcesRoot, pythonPayloadArchiveName)); !os.IsNotExist(err) {
		t.Fatalf("payload archive should be removed after extraction: %v", err)
	}

	if err := os.WriteFile(pythonPath, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareBundledPythonRuntime(paths); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(pythonPath); err != nil || string(body) != "kept" {
		t.Fatalf("second preparation should be a no-op; got %q, %v", body, err)
	}
}

func TestPrepareBundledPythonRuntimeRejectsUnsafePayloadPath(t *testing.T) {
	resourcesRoot := t.TempDir()
	writePythonPayloadArchive(t, resourcesRoot, map[string]string{
		"runtimes/python/python.exe": "python",
		"deps/python/venv/file":      "venv",
		"../outside.txt":             "unsafe",
	})
	err := prepareBundledPythonRuntime(RuntimePaths{ResourcesRoot: resourcesRoot})
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("prepare error = %v, want unsafe path", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(resourcesRoot), "outside.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe payload escaped staging root: %v", statErr)
	}
}

func TestPrepareBundledPythonRuntimeWithoutArchiveIsNoOp(t *testing.T) {
	if err := prepareBundledPythonRuntime(RuntimePaths{ResourcesRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}
