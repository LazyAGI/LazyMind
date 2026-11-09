//go:build windows

package agentexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsPackageQueryHasNoConsole(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	// This verifies console flags, not the production package-query deadline.
	// A cold PowerShell + Add-Type compiler can exceed 8s on a busy CI runner.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := windowsAppPackageCommand(ctx, system, []string{"OpenAI.Codex"})
	if !command.SysProcAttr.HideWindow || command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("background discovery may create a console")
	}
	// Exercise the actual process flags, without scanning installed applications.
	command.Args[len(command.Args)-1] = `Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class ConsoleProbe { [DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow(); }'; [ConsoleProbe]::GetConsoleWindow().ToInt64()`
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("console probe: %v (context: %v): %s", err, ctx.Err(), body)
	}
	if strings.TrimSpace(string(body)) != "0" {
		t.Fatalf("background PowerShell owns console window: %s", body)
	}
}

func TestPackageCacheAcrossProcesses(t *testing.T) {
	if directory := os.Getenv("LAZYMIND_TEST_PACKAGE_CACHE_HELPER"); directory != "" {
		cache := desktopPackageCache{directory, time.Now}
		_, err := cache.query(context.Background(), []string{"OpenAI.Codex"}, func(context.Context, []string) ([]byte, error) {
			f, err := os.OpenFile(filepath.Join(directory, "queries"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return nil, err
			}
			_, err = f.WriteString("query\n")
			_ = f.Close()
			return []byte(`[]`), err
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	directory := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestPackageCacheAcrossProcesses$")
			command.SysProcAttr = windowsAppPackageCommand(ctx, "", nil).SysProcAttr
			command.Env = append(os.Environ(), "LAZYMIND_TEST_PACKAGE_CACHE_HELPER="+directory)
			if output, err := command.CombinedOutput(); err != nil {
				t.Errorf("helper: %v %s", err, output)
			}
		}()
	}
	wg.Wait()
	body, err := os.ReadFile(filepath.Join(directory, "queries"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "query\n" {
		t.Fatalf("duplicated process scans: %s", body)
	}
}

func TestDesktopBindingInvalidatesSharedPackageCache(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "home"))
	directory, err := desktopPackageCacheDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cache := desktopPackageCache{directory, time.Now}
	calls := 0
	query := func(context.Context, []string) ([]byte, error) { calls++; return []byte(`[]`), nil }
	check := func(want int) {
		t.Helper()
		if _, err := cache.query(context.Background(), []string{"OpenAI.Codex"}, query); err != nil {
			t.Fatal(err)
		}
		if calls != want {
			t.Fatalf("queries=%d want %d", calls, want)
		}
	}
	check(1)
	executable := filepath.Join(root, "Selected.exe")
	if err := os.WriteFile(executable, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetExecutableBinding(CodexDesktop, executable); err != nil {
		t.Fatal(err)
	}
	check(2)
	if err := ClearExecutableBinding(CodexDesktop); err != nil {
		t.Fatal(err)
	}
	check(3)
}

func TestExplicitBindingsSurviveUnavailablePackageCache(t *testing.T) {
	for _, target := range []BindingTarget{CodexDesktop, CodexCLI} {
		t.Run(string(target), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LOCALAPPDATA", root)
			t.Setenv("LAZYMIND_HOME", filepath.Join(root, "home"))
			// A file blocks creation of the cache directory without relying on
			// Windows permission semantics or an elevated test user's privileges.
			if err := os.WriteFile(filepath.Join(root, "LazyMind"), []byte("blocked"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := InvalidateDesktopApplications(context.Background()); err == nil {
				t.Fatal("fixture must make the package cache unavailable")
			}
			executable := filepath.Join(root, "Selected.exe")
			body := "fixture"
			if isCLIBindingTarget(target) {
				// CLI bindings execute --version; use a runnable Windows script.
				executable = filepath.Join(root, "Selected.cmd")
				body = "@echo off\r\necho 1.0.0\r\n"
			}
			if err := os.WriteFile(executable, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := SetExecutableBinding(target, executable); err != nil {
				t.Fatalf("valid explicit binding rejected because cache is unavailable: %v", err)
			}
			path, found, err := ExecutableBinding(target)
			if err != nil || !found {
				t.Fatalf("binding not persisted: path=%q found=%v err=%v", path, found, err)
			}
			// ResolveRunnable/ResolveDesktopApplication canonicalize paths. Compare
			// file identity, not Windows path casing or separator spelling.
			want, err := os.Stat(executable)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.Stat(path)
			if err != nil || !os.SameFile(want, got) {
				t.Fatalf("binding points to a different file: path=%q want=%q err=%v", path, executable, err)
			}
			if err := ClearExecutableBinding(target); err != nil {
				t.Fatalf("clearing binding depends on package cache: %v", err)
			}
			if _, found, err := ExecutableBinding(target); err != nil || found {
				t.Fatalf("binding not cleared: found=%v err=%v", found, err)
			}
		})
	}
}
