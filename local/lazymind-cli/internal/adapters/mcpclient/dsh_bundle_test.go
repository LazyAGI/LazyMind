package mcpclient

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMissingDSHNeverInvokesPackageInstaller(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", root)
	t.Setenv("LAZYMIND_DSH_PATH", filepath.Join(root, "missing-dsh"))
	err := runDSHPlugin(context.Background(), "web", "add", "plugin.tgz")
	if err == nil || !strings.Contains(err.Error(), "select your existing DSH executable") {
		t.Fatalf("missing DSH did not fail explicitly: %v", err)
	}
}

func TestMissingPNPMKeepsTheExistingDSHExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture; Windows command invocation has separate native tests")
	}
	root := t.TempDir()
	setTestHome(t, root)
	t.Setenv("LAZYMIND_HOME", root)
	t.Setenv("SHELL", "")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dsh := filepath.Join(bin, "existing dsh")
	if err := os.WriteFile(dsh, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_DSH_PATH", dsh)
	if err := os.WriteFile(filepath.Join(bin, "npx"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOME/dsh-arguments\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := runDSHPlugin(context.Background(), "web", "add", "plugin.tgz"); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(filepath.Join(root, "dsh-arguments"))
	if err != nil {
		t.Fatal(err)
	}
	dsh, err = filepath.EvalSymlinks(dsh)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "@deepseek-ai/dsh") || !strings.Contains(string(args), "\n"+dsh+"\nplugin\n--profile\nweb\n") {
		t.Fatalf("installer selected another DSH: %s", args)
	}
}
