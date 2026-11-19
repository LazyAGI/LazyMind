package codexplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
)

// The copied Windows test executable is a native node.exe fixture, including
// paths with spaces; no shell shim or system Node installation is required.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" && len(os.Args) == 2 && os.Args[1] == "--version" && strings.EqualFold(filepath.Base(os.Args[0]), "node.exe") {
		if filepath.Base(filepath.Dir(os.Args[0])) == "old node" {
			fmt.Println("v20.19.0")
		} else {
			fmt.Println("v24.22.3")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func nodeFixture(t *testing.T, dir, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		return executable(t, dir, "node", "#!/bin/sh\necho "+version+"\n")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	return executable(t, dir, "node.exe", string(body))
}

func TestFindNodeTriesLaterSystemInstallations(t *testing.T) {
	root := t.TempDir()
	t.Setenv(connectorconfig.EnvironmentKey, "")
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LAZYMIND_HOST_HOME", root)
	old := nodeFixture(t, filepath.Join(root, "old node"), "v20.19.0")
	want := nodeFixture(t, filepath.Join(root, "Program Files", "nodejs"), "v24.22.3")
	t.Setenv("LAZYMIND_NODE_BIN", "")
	t.Setenv("PATH", filepath.Dir(old)+string(os.PathListSeparator)+filepath.Dir(want))
	t.Setenv("SHELL", "")
	want, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FindNode(t.Context())
	if err != nil || got != want {
		t.Fatalf("later compatible system Node ignored: got %q, want %q, err %v", got, want, err)
	}
	// An explicit override remains authoritative rather than silently ignored.
	t.Setenv("LAZYMIND_NODE_BIN", old)
	if _, err := FindNode(t.Context()); err == nil {
		t.Fatal("accepted incompatible explicit override")
	}
}

func TestFindNodeAfterHomebrewLinkReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew symbolic link layout")
	}
	root := t.TempDir()
	t.Setenv(connectorconfig.EnvironmentKey, "")
	old := nodeFixture(t, filepath.Join(root, "Cellar", "node", "old"), "v24.22.3")
	want := nodeFixture(t, filepath.Join(root, "Cellar", "node", "new"), "v26.8.2")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(want, filepath.Join(bin, "node")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_NODE_BIN", "")
	t.Setenv("PATH", bin)
	t.Setenv("SHELL", "")
	want, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := findNode(t.Context(), old)
	if err != nil || got != want {
		t.Fatalf("stale Cellar path blocked updated Homebrew link: %q %v", got, err)
	}
}
