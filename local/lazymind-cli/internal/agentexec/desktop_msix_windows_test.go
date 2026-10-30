package agentexec

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Exercise Windows PowerShell's actual JSON/XML behavior without installing a
// package or changing registry entries on the test machine.
func TestWindowsAppPackageQueryProtocol(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fixture string
		count         int
		fails         bool
	}{
		{"installed", `function Get-AppxPackage { [PSCustomObject]@{Name='OpenAI.Codex'; InstallLocation='C:\Program Files\WindowsApps\dynamic-version'; PackageFullName='fixture'; Version=[version]'2.0'} }
function Get-AppxPackageManifest { [xml]'<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10"><Applications><Application Id="App" Executable="app\ChatGPT.exe" /></Applications></Package>' }`, 1, false},
		{"not-installed", `function Get-AppxPackage { }`, 0, false},
		{"query-failed", `function Get-AppxPackage { throw 'fixture query error' }`, 0, true},
		{"manifest-failed", `function Get-AppxPackage { [PSCustomObject]@{Name='OpenAI.Codex'; PackageFullName='fixture'} }
function Get-AppxPackageManifest { throw 'fixture manifest error' }`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"),
				"-NoProfile", "-NonInteractive", "-Command", tc.fixture+"\n"+windowsAppPackageQuery([]string{"OpenAI.Codex"}))
			body, err := command.Output()
			if tc.fails {
				if err == nil {
					t.Fatal("query failure returned success")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var packages []windowsAppPackage
			if err := json.Unmarshal(body, &packages); err != nil || len(packages) != tc.count {
				t.Fatalf("packages=%s, err=%v", body, err)
			}
			if tc.count > 0 && (len(packages[0].Executables) != 1 || packages[0].Executables[0] != `app\ChatGPT.exe`) {
				t.Fatalf("unexpected manifest executables: %+v", packages)
			}
		})
	}
}

func TestWindowsAppPackageSystemQueryMissingIdentity(t *testing.T) {
	body, err := queryWindowsAppPackages([]string{"LazyMind.Nonexistent.Discovery.Test.Package"})
	if err != nil {
		t.Fatal(err)
	}
	var packages []windowsAppPackage
	if err := json.Unmarshal(body, &packages); err != nil || len(packages) != 0 {
		t.Fatalf("packages=%s, err=%v", body, err)
	}
}
