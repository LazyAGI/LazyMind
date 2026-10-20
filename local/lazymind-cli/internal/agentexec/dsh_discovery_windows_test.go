//go:build windows

package agentexec

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestFindDSHDesktopCLIWithoutOverrides(t *testing.T) {
	for _, source := range []string{"Programs", "InstallLocation", "DisplayIcon", "UninstallString"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LAZYMIND_HOME", filepath.Join(root, "LazyMind"))
			t.Setenv("LAZYMIND_DSH_DESKTOP_PATH", "")
			t.Setenv("LAZYMIND_DSH_PATH", "")
			t.Setenv("LOCALAPPDATA", root)
			t.Setenv("ProgramFiles", "")
			t.Setenv("ProgramFiles(x86)", "")
			install := filepath.Join(root, "自定义 安装")
			if source == "Programs" {
				install = filepath.Join(root, "Programs", "DeepSeek Harness")
			}
			// A similarly named launcher without the Desktop bundle must not win.
			invalid := filepath.Join(root, "Programs", "A incomplete", "resources", "runtime", "cli", "bin", "dsh.cmd")
			if err := os.MkdirAll(filepath.Dir(invalid), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(invalid, []byte("@echo off\r\n"), 0600); err != nil {
				t.Fatal(err)
			}

			cli := filepath.Join(install, "resources", "runtime", "cli", "bin", "dsh.cmd")
			if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{
				cli: "@echo off\r\n",
				filepath.Join(install, "resources", "app.asar"): "fixture",
			} {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if source != "Programs" {
				keyPath := fmt.Sprintf(`Software\Microsoft\Windows\CurrentVersion\Uninstall\LazyMindTestDSH-%d`, time.Now().UnixNano())
				key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { registry.DeleteKey(registry.CURRENT_USER, keyPath) })
				defer key.Close()
				if err := key.SetStringValue("DisplayName", "DeepSeek Harness Test"); err != nil {
					t.Fatal(err)
				}
				value := install
				if source == "DisplayIcon" {
					value = `"` + filepath.Join(install, "DeepSeek Harness.exe") + `",0`
				}
				if source == "UninstallString" {
					value = `"` + filepath.Join(install, "Uninstall DeepSeek Harness.exe") + `" /S`
				}
				if err := key.SetStringValue(source, value); err != nil {
					t.Fatal(err)
				}
			}
			got, err := FindDSHDesktopCLI()
			if err != nil || !SameExecutable(got, cli) {
				t.Fatalf("discovery: %q, %v", got, err)
			}
		})
	}
}
