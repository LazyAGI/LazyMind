package agentexec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" && strings.EqualFold(filepath.Base(os.Args[0]), "node.exe") {
		fmt.Println("v24.22.3")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCompatibleNodeRejectsBatchShimsAndUsesNativeExecutable(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "Program Files", "nodejs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(dir, "node.exe")
	if err := os.WriteFile(native, body, 0700); err != nil {
		t.Fatal(err)
	}
	for _, extension := range []string{".CMD", ".bat"} {
		t.Run(extension, func(t *testing.T) {
			shim := filepath.Join(t.TempDir(), "node"+extension)
			if err := os.WriteFile(shim, []byte("@echo off\r\necho v24.22.3\r\n"), 0700); err != nil {
				t.Fatal(err)
			}
			// Establish that the generic CLI runner accepts the shim, which was
			// previously enough for Node discovery to report it as compatible.
			if output, err := Run(t.Context(), shim, "--version"); err != nil || strings.TrimSpace(output) != "v24.22.3" {
				t.Fatalf("batch version fixture failed: %q %v", output, err)
			}
			if got, err := CompatibleNode(t.Context(), shim); err == nil {
				t.Fatalf("accepted batch shim for direct Node startup: %q", got)
			}
			if got, err := CompatibleNode(t.Context(), shim, native); err != nil || !SameExecutable(got, native) {
				t.Fatalf("did not continue to native Node: %q %v", got, err)
			}
		})
	}
}
