package agentexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveDSHDesktopCLI(t *testing.T) {
	app := filepath.Join(t.TempDir(), "应用 space.app")
	resources := filepath.Join(app, "Contents", "Resources")
	launcher := "dsh"
	if runtime.GOOS == "windows" {
		resources = filepath.Join(t.TempDir(), "应用 space", "resources")
		launcher = "dsh.cmd"
	}
	cli := filepath.Join(resources, "runtime", "cli", "bin", launcher)
	if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n"
	if runtime.GOOS == "windows" {
		script = "@echo off\r\n"
	}
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "app.asar"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(cli)
	inputs := []string{cli}
	if runtime.GOOS != "windows" {
		inputs = append(inputs, app)
	}
	for _, value := range inputs {
		got, err := ResolveDSHDesktopCLI(value)
		if err != nil || got != want {
			t.Fatalf("resolve %s: %s %v", value, got, err)
		}
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), launcher)
		if err := os.Symlink(cli, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink creation is unavailable: %v", err)
			}
			t.Fatal(err)
		}
		if got, err := ResolveDSHDesktopCLI(link); err != nil || got != want {
			t.Fatalf("symlink: %s %v", got, err)
		}
	})
	t.Setenv("LAZYMIND_DSH_DESKTOP_PATH", inputs[len(inputs)-1])
	t.Setenv("LAZYMIND_DSH_PATH", "/old/npm/dsh")
	if got, err := FindDSHDesktopCLI(); err != nil || got != want {
		t.Fatalf("explicit Desktop: %s %v", got, err)
	}
	if err := os.Remove(filepath.Join(resources, "app.asar")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDSHDesktopCLI(cli); err == nil {
		t.Fatal("incomplete installation accepted")
	}
	if _, err := ResolveDSHDesktopCLI("/bin/sh"); err == nil {
		t.Fatal("ordinary executable accepted as Desktop")
	}
}
