//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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

func TestRelocateDesktopPythonVenvsSkipsUnchangedReadOnlyFiles(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		PythonRuntimeDir:   filepath.Join(root, "runtimes", "python"),
		AuthServiceVenvDir: filepath.Join(root, "deps", "python", "auth-service"),
		AlgorithmVenv:      filepath.Join(root, "deps", "python", "algorithm-missing"),
	}
	homeName := "cpython-3.11-windows-x86_64-none"
	home := filepath.Join(paths.PythonRuntimeDir, homeName)
	venv := paths.AuthServiceVenvDir
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(venv, "Scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	pythonPath := filepath.Join(venv, "Scripts", "python.exe")
	configPath := filepath.Join(venv, "pyvenv.cfg")
	if err := os.WriteFile(filepath.Join(home, "python.exe"), []byte("bundled-python"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pythonPath, []byte("bundled-python"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("home = "+home+"\nversion_info = 3.11\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pythonPath, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(pythonPath, 0o644)
		_ = os.Chmod(configPath, 0o644)
	})
	if err := relocateDesktopPythonVenvs(RuntimeConfig{Profile: "desktop"}, paths); err != nil {
		t.Fatalf("unchanged relocation: %v", err)
	}
}

func TestReplaceRelocatableFileReportsWindowsFileLock(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "python.exe")
	if err := os.WriteFile(destination, []byte("old-python"), 0o755); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock destination: %v", err)
	}
	defer windows.CloseHandle(handle)

	err = replaceRelocatableFileIfChanged(destination, []byte("new-python"), 0o755, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected locked file replacement to fail")
	}
	if !strings.Contains(err.Error(), "still locked") {
		t.Fatalf("replacement error = %q, want actionable lock detail", err)
	}
}
