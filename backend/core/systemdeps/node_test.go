package systemdeps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectNodeDoesNotProbeDesktopPPTExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	dir := t.TempDir()
	desktop := filepath.Join(dir, "desktop-app")
	marker := filepath.Join(dir, "desktop-started")
	if err := os.WriteFile(desktop, []byte("#!/bin/sh\n: > \"$NODE_TEST_PROBE_MARKER\"\nprintf 'v41.0.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_NODE_BIN", "")
	t.Setenv("LAZYMIND_NODE_EXECUTABLE", desktop)
	t.Setenv("LAZYMIND_NODE_RUN_AS_NODE", "true")
	t.Setenv("NODE_TEST_PROBE_MARKER", marker)
	status := detectNode(context.Background())
	if status.Path == desktop {
		t.Error("accepted the desktop application as a standalone Node.js runtime")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("Node.js detection launched the desktop application: %v", err)
	}
}

func TestCompatibleNodeVersion(t *testing.T) {
	for version, want := range map[string]bool{"v22.18.0": false, "v22.19.0": true, "v24.22.3": true, "v20.0.0": false, "invalid": false} {
		if got := compatibleNodeVersion(version); got != want {
			t.Errorf("%s: got %v, want %v", version, got, want)
		}
	}
}

func TestExtractNodeExecutable(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		for _, valid := range []bool{false, true} {
			dir := t.TempDir()
			archive := filepath.Join(dir, "archive")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			name := "package/bin/node"
			member := name
			if !valid {
				name = "../unexpected"
			}
			if zipped {
				writer := zip.NewWriter(file)
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte("node executable")); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				gz := gzip.NewWriter(file)
				writer := tar.NewWriter(gz)
				if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: 15, Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write([]byte("node executable")); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := gz.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(dir, "node")
			err = extractNodeExecutable(archive, member, destination, zipped)
			if !valid {
				if err == nil {
					t.Fatal("accepted an unexpected archive member")
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("unexpected output created")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(destination)
			if err != nil || string(content) != "node executable" {
				t.Fatalf("incorrect extracted content: %q, %v", content, err)
			}
		}
	}
}
