package codexruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func launchFixture(t *testing.T) (string, *runtimeConfig) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &runtimeConfig{Version: 2, RuntimeDir: filepath.Join(root, "runtime"), BundleDir: filepath.Join(root, "bundle"), CodexHome: root,
		CLI: filepath.Join(root, "old", "app", "resources", "codex.exe"), Node: filepath.Join(root, "old", "app", "resources", "cua_node", "bin", "node.exe"),
		DesktopExecutable: filepath.Join(root, "old", "app", "ChatGPT.exe"), Helper: filepath.Join(root, "helper.exe")}
	for _, file := range []string{c.CLI, c.Node, c.DesktopExecutable, c.Helper, filepath.Join(c.BundleDir, "adapter.mjs")} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := privateDirectory(c.RuntimeDir); err != nil {
		t.Fatal(err)
	}
	c.Endpoint = "ws://127.0.0.1:51337"
	c.TokenFile = filepath.Join(root, "token")
	if err := writePrivateJSON(c.TokenFile, "fixture"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "runtime.json")
	if err := writePrivateJSON(file, c); err != nil {
		t.Fatal(err)
	}
	return file, c
}

func TestWindowsUpgradeDelegatesBeforeStartingRemovedNode(t *testing.T) {
	file, c := launchFixture(t)
	app := filepath.Join(filepath.Dir(file), "new-package", "app")
	for _, name := range []string{"ChatGPT.exe", "Codex.exe", "resources/codex.exe"} {
		p := filepath.Join(app, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `<Package><Identity Name="OpenAI.Codex"/><Applications><Application Executable="app/ChatGPT.exe"/></Applications></Package>`
	if err := os.WriteFile(filepath.Join(filepath.Dir(app), "AppxManifest.xml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(filepath.Dir(file), "old")); err != nil {
		t.Fatal(err)
	}
	desktop := filepath.Join(app, "ChatGPT.exe")
	for _, args := range [][]string{{"app-server", "--stdio"}, {"--version"}, {"app-server", "generate-json-schema"}} {
		cmd, err := windowsLaunchCommand(context.Background(), file, args, desktop)
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Path != filepath.Join(app, "resources", "codex.exe") || !reflect.DeepEqual(cmd.Args[1:], args) {
			t.Fatalf("lost native command: %v", cmd.Args)
		}
	}
	if got := ConnectionState(&Registration{Installation: file}); got != "invalidated" {
		t.Fatal(got)
	}
	if _, err := os.Stat(c.Node); !os.IsNotExist(err) {
		t.Fatal("test did not remove old Node")
	}
	// CLI-only and conflicting layouts cannot become a recovery target.
	if _, err := windowsCLIForDesktop(filepath.Join(app, "Codex.exe")); err == nil {
		t.Fatal("accepted launcher instead of manifest entry")
	}
	if err := os.Remove(desktop); err != nil {
		t.Fatal(err)
	}
	if _, err := windowsLaunchCommand(context.Background(), file, []string{"app-server"}, desktop); err == nil {
		t.Fatal("accepted missing desktop")
	}
}

func TestWindowsLaunchPreservesPolicyArgsAndProfileWithoutNode(t *testing.T) {
	file, c := launchFixture(t)
	_ = os.Remove(c.Node)
	args := []string{"--config", "policy=value", "login", "status"}
	cmd, err := windowsLaunchCommand(context.Background(), file, args, c.DesktopExecutable)
	if err != nil || cmd.Path != c.CLI || !reflect.DeepEqual(cmd.Args[1:], args) {
		t.Fatalf("cmd=%v err=%v", cmd, err)
	}
	env := officialCLIEnvironment([]string{"CODEX_HOME=profile", "HTTPS_PROXY=proxy", "codex_cli_path=old", "CODEX_APP_SERVER_WS_URL=old", "LAZYMIND_RUNTIME_HELPER=old"}, c.CLI)
	if strings.Join(env, "|") != "CODEX_HOME=profile|HTTPS_PROXY=proxy|CODEX_CLI_PATH="+c.CLI {
		t.Fatal(env)
	}
	got := launcherDesktop(3, func(pid int) (*ProcessInfo, error) {
		if pid == 3 {
			return &ProcessInfo{PPID: 2, Executable: c.Node}, nil
		}
		return &ProcessInfo{PPID: 1, Executable: c.DesktopExecutable}, nil
	})
	if got != c.DesktopExecutable {
		t.Fatal(got)
	}
}

func TestRestoreStartupWithoutNodePreservesSnapshotAndExternalChanges(t *testing.T) {
	file, c := launchFixture(t)
	_ = os.Remove(c.Node)
	previous, launcher, force := "original-cli", filepath.Join(filepath.Dir(file), "launch-adapter.exe"), "1"
	before := environmentSnapshot{Values: map[string]*string{"CODEX_CLI_PATH": &previous, "CODEX_APP_SERVER_FORCE_CLI": nil, "CODEX_APP_SERVER_WS_URL": nil, "CODEX_APP_SERVER_USE_LOCAL_DAEMON": nil}, Types: map[string]uint32{"CODEX_CLI_PATH": 2}}
	current := environmentSnapshot{Values: map[string]*string{"CODEX_CLI_PATH": &launcher, "CODEX_APP_SERVER_FORCE_CLI": &force, "CODEX_APP_SERVER_WS_URL": nil, "CODEX_APP_SERVER_USE_LOCAL_DAEMON": nil}, Types: map[string]uint32{"CODEX_CLI_PATH": 1, "CODEX_APP_SERVER_FORCE_CLI": 1}}
	stateFile := filepath.Join(filepath.Dir(file), "activation.json")
	if err := writePrivateJSON(stateFile, map[string]any{"config": file, "before": before}); err != nil {
		t.Fatal(err)
	}
	writes := 0
	environment := func(values map[string]*string, types map[string]uint32) (*environmentSnapshot, error) {
		if values != nil {
			writes++
			current = environmentSnapshot{Values: values, Types: types}
		}
		return &current, nil
	}
	external := "user-change"
	current.Values["CODEX_CLI_PATH"] = &external
	if err := restoreStartupSnapshot(file, environment); err == nil || writes != 0 {
		t.Fatal("overwrote external change")
	}
	current.Values["CODEX_CLI_PATH"] = &launcher
	if err := restoreStartupSnapshot(file, environment); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current, before) || !startupRestoring(file) {
		t.Fatal("snapshot or recovery marker lost")
	}
	if err := restoreStartupSnapshot(file, environment); err != nil {
		t.Fatal("recovery not retryable", err)
	}
}

func TestConnectionStateDoesNotTreatRegistrationAsConnection(t *testing.T) {
	if ConnectionState(nil) != "unconfigured" {
		t.Fatal("missing registration")
	}
	file, c := launchFixture(t)
	r := &Registration{Installation: file, Profile: c.CodexHome, Endpoint: c.Endpoint}
	if got := ConnectionState(r); got != "waiting_restart" {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(c.RuntimeDir, "adapter.jsonl"), []byte("failed startup"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ConnectionState(r); got != "failed" {
		t.Fatal(got)
	}
	if err := os.Remove(c.CLI); err != nil {
		t.Fatal(err)
	}
	if got := ConnectionState(r); got != "invalidated" {
		t.Fatal(got)
	}
}

func TestWindowsLaunchUsesSharedAdapterOnlyForMatchingValidatedRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX version-command fixture; Windows launch decisions covered by directory fixtures")
	}
	file, c := launchFixture(t)
	c.CLIVersion = "fixture-version"
	if err := os.WriteFile(c.CLI, []byte("#!/bin/sh\necho codex-cli fixture-version\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(file, c); err != nil {
		t.Fatal(err)
	}
	cmd, err := windowsLaunchCommand(context.Background(), file, []string{"app-server"}, c.DesktopExecutable)
	if err != nil || cmd.Path != c.Node {
		t.Fatalf("healthy runtime: cmd=%v err=%v", cmd, err)
	}
	c.CLIVersion = "old-version"
	if err := writePrivateJSON(file, c); err != nil {
		t.Fatal(err)
	}
	cmd, err = windowsLaunchCommand(context.Background(), file, []string{"app-server"}, c.DesktopExecutable)
	if err != nil || cmd.Path != c.CLI {
		t.Fatalf("changed version: cmd=%v err=%v", cmd, err)
	}
	if _, err := os.Stat(filepath.Join(c.RuntimeDir, "startup-invalid.json")); err != nil {
		t.Fatal("missing reconfiguration notice", err)
	}
	c.CLIVersion = "fixture-version"
	if err := writePrivateJSON(file, c); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(filepath.Join(filepath.Dir(file), "activation.json"), map[string]bool{"disabling": true}); err != nil {
		t.Fatal(err)
	}
	cmd, err = windowsLaunchCommand(context.Background(), file, []string{"app-server"}, c.DesktopExecutable)
	if err != nil || cmd.Path != c.CLI {
		t.Fatalf("recovery must ignore inherited stale environment: cmd=%v err=%v", cmd, err)
	}
}

func TestDesktopConnectionRequiresLiveAdapterDesktopAndMatchingBackend(t *testing.T) {
	_, c := launchFixture(t)
	backend := &backendRecord{Process: &ProcessInfo{Identity: "current-backend"}}
	adapter := &ProcessInfo{Identity: "adapter"}
	desktop := &ProcessInfo{Identity: "desktop", Executable: c.DesktopExecutable}
	file := filepath.Join(c.RuntimeDir, "desktop-123.json")
	write := func(id string) {
		t.Helper()
		if err := writePrivateJSON(file, map[string]any{"process": adapter, "desktop": desktop, "backend": &ProcessInfo{Identity: id}}); err != nil {
			t.Fatal(err)
		}
	}
	alive := func(p *ProcessInfo) bool { return p != nil && p.Identity != "dead" }
	write("current-backend")
	if !desktopConnected(c, backend, alive) {
		t.Fatal("live transport not recognized")
	}
	write("previous-backend")
	if desktopConnected(c, backend, alive) {
		t.Fatal("stale backend accepted")
	}
	adapter.Identity = "dead"
	write("current-backend")
	if desktopConnected(c, backend, alive) {
		t.Fatal("dead adapter accepted")
	}
	adapter.Identity = "adapter"
	desktop.Identity = "dead"
	write("current-backend")
	if desktopConnected(c, backend, alive) {
		t.Fatal("closed desktop accepted")
	}
}
