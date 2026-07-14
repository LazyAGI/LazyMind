//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelocateDesktopPythonVenvs(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		PythonRuntimeDir:   filepath.Join(root, "runtimes", "python"),
		AuthServiceVenvDir: filepath.Join(root, "deps", "python", "auth-service"),
		AlgorithmVenv:      filepath.Join(root, "deps", "python", "algorithm"),
	}
	homeName := "cpython-3.11-windows-x86_64-none"
	home := filepath.Join(paths.PythonRuntimeDir, homeName)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "python.exe"), []byte("bundled-python"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, venv := range []string{paths.AuthServiceVenvDir, paths.AlgorithmVenv} {
		if err := os.MkdirAll(venv, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(venv, "Scripts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(venv, "Scripts", "python.exe"), []byte("uv-trampoline"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(venv, "pyvenv.cfg"), []byte("home = C:\\build\\"+homeName+"\nversion_info = 3.11\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := relocateDesktopPythonVenvs(RuntimeConfig{Profile: "desktop"}, paths); err != nil {
		t.Fatal(err)
	}
	for _, venv := range []string{paths.AuthServiceVenvDir, paths.AlgorithmVenv} {
		raw, err := os.ReadFile(filepath.Join(venv, "pyvenv.cfg"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "home = "+home) {
			t.Fatalf("relocated config = %q, want home %q", raw, home)
		}
		python, err := os.ReadFile(filepath.Join(venv, "Scripts", "python.exe"))
		if err != nil {
			t.Fatal(err)
		}
		if string(python) != "bundled-python" {
			t.Fatalf("relocated Python executable = %q", python)
		}
	}
}
