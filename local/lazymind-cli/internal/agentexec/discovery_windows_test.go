//go:build windows

package agentexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestWindowsPathResolutionSupportsPATHEXTCmdFiles(t *testing.T) {
	directory := t.TempDir()
	command := filepath.Join(directory, "custom-agent.cmd")
	if err := os.WriteFile(command, []byte("@echo off\r\necho %~1\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lookPathIn("custom-agent", directory, []string{".exe", ".cmd"}); got != command {
		t.Fatalf("resolved=%q want=%q", got, command)
	}
	resolved, err := ResolveExecutable(filepath.Join(directory, "custom-agent"))
	if err != nil || !SameExecutable(resolved, command) {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := Run(ctx, resolved, "hello world")
	if err != nil || strings.TrimSpace(output) != "hello world" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestWindowsPathMergeIncludesNewUserPathAndExecutionAliases(t *testing.T) {
	value := mergeWindowsPath(
		`C:\Process`, `C:\Machine`, `D:\User;C:\Process`, `D:\Profiles\User\AppData\Local`,
	)
	parts := filepath.SplitList(value)
	want := []string{
		`C:\Process`, `C:\Machine`, `D:\User`,
		`D:\Profiles\User\AppData\Local\Microsoft\WindowsApps`,
	}
	if len(parts) != len(want) {
		t.Fatalf("PATH=%#v", parts)
	}
	for index := range want {
		if !strings.EqualFold(parts[index], want[index]) {
			t.Fatalf("PATH[%d]=%q want=%q", index, parts[index], want[index])
		}
	}
}

func TestWindowsRegistryCommandKeepsUnquotedExecutablePathWithSpaces(t *testing.T) {
	command := `C:\Program Files\LazyMind Agent\agent.exe,0`
	if got, want := commandExecutable(command), `C:\Program Files\LazyMind Agent\agent.exe`; got != want {
		t.Fatalf("commandExecutable(%q)=%q want %q", command, got, want)
	}
}

func TestWindowsDesktopDiscoveryUsesRegisteredProtocol(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "Custom Cursor.exe")
	if err := os.WriteFile(executable, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	protocol := fmt.Sprintf("lazymind-test-%d", time.Now().UnixNano())
	keyPath := `Software\Classes\` + protocol + `\shell\open\command`
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.SetStringValue("", `"`+executable+`" "%1"`); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	_ = key.Close()
	t.Cleanup(func() {
		for _, path := range []string{
			keyPath,
			`Software\Classes\` + protocol + `\shell\open`,
			`Software\Classes\` + protocol + `\shell`,
			`Software\Classes\` + protocol,
		} {
			_ = registry.DeleteKey(registry.CURRENT_USER, path)
		}
	})

	state, err := InspectDesktopApplication(DesktopApplication{Protocols: []string{protocol}})
	if err != nil || !state.Installed {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}

func TestWindowsCommandDiscoveryUsesAppPaths(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "custom-agent.exe")
	if err := os.WriteFile(executable, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("lazymind-agent-%d.exe", time.Now().UnixNano())
	keyPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\` + name
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.SetStringValue("", executable); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	_ = key.Close()
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, keyPath) })

	candidates := platformExecutableCandidates([]string{strings.TrimSuffix(name, ".exe")})
	if len(candidates) == 0 || !strings.EqualFold(candidates[0], executable) {
		t.Fatalf("candidates=%#v want=%q", candidates, executable)
	}
}

func TestWindowsCommandDiscoveryFindsPackagedCLIWithoutProductPathRules(t *testing.T) {
	localAppData := t.TempDir()
	command := filepath.Join(localAppData, "Programs", "Vendor App", "resources", "custom-agent.cmd")
	if err := os.MkdirAll(filepath.Dir(command), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("@echo off\r\necho 1.0.0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("PATH", "")

	candidates := platformExecutableCandidates([]string{"custom-agent"})
	if len(candidates) == 0 || !strings.EqualFold(candidates[0], command) {
		t.Fatalf("candidates=%#v want=%q", candidates, command)
	}
}

func TestWindowsCommandDiscoveryFindsCLIAlongsideBoundDesktopApplication(t *testing.T) {
	root := t.TempDir()
	desktop := filepath.Join(root, "Custom App.exe")
	command := filepath.Join(root, "resources", "custom-agent.cmd")
	if err := os.WriteFile(desktop, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(command), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("@echo off\r\necho 1.0.0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "empty-local-app-data"))
	t.Setenv("PATH", "")
	if _, err := SetExecutableBinding(CodexDesktop, desktop); err != nil {
		t.Fatal(err)
	}

	candidates := platformExecutableCandidates([]string{"custom-agent"})
	if len(candidates) == 0 || !strings.EqualFold(candidates[0], command) {
		t.Fatalf("candidates=%#v want=%q", candidates, command)
	}
}

func TestWindowsDesktopDiscoveryUsesUninstallRegistration(t *testing.T) {
	installDirectory := t.TempDir()
	displayName := fmt.Sprintf("LazyMind Test Agent %d", time.Now().UnixNano())
	registeredName := displayName + " (User)"
	keyPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + registeredName
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.SetStringValue("DisplayName", registeredName); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	if err := key.SetStringValue("InstallLocation", installDirectory); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	_ = key.Close()
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, keyPath) })

	state, err := InspectDesktopApplication(DesktopApplication{DisplayNames: []string{displayName}})
	if err != nil || !state.Installed {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}
