//go:build darwin || linux

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRuntimeCleanupDoesNotSignalHostProcessGroup(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, BuildRoot: root, RuntimeRoot: filepath.Join(root, "runtime"), ProcessRegistryFile: filepath.Join(root, "run", "processes.json")}
	proxy := newCleanupFixture(t, filepath.Join(root, "bin", "lazymind"), "", nil)
	worker := newCleanupFixture(t, filepath.Join(root, "bin", "runtime-worker"), paths.RuntimeRoot, func(cmd *exec.Cmd) {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: proxy.cmd.Process.Pid}
	})
	if processGroupID(worker.cmd.Process.Pid) != proxy.cmd.Process.Pid {
		t.Fatal("worker did not join the proxy's process group")
	}
	manager := NewRuntimeManager(&fakeRunner{t: t}, "")
	manager.SetOutput(io.Discard, io.Discard)
	if err := manager.killStaleRuntimeProcesses(context.Background(), RuntimeConfig{}, paths); err != nil {
		t.Fatal(err)
	}
	proxy.assertTools(t, 2)
	if processAlive(worker.cmd.Process.Pid) {
		t.Fatal("owned worker survived cleanup")
	}
}

func TestDesktopGuardCleansWorkersAfterAppRemovalDuringStartup(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "LazyMind.app")
	resources := filepath.Join(appRoot, "Contents", "Resources", "runtime")
	repo := filepath.Join(resources, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfigWithOptions(RuntimeConfigOptions{
		Profile: "desktop", OwnerToken: "fixture-desktop-owner", RepoRoot: repo,
		ResourcesRoot: resources, RuntimeRoot: filepath.Join(root, "user-data"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(paths.RuntimeRoot, "preserved.db")
	if err := os.WriteFile(sentinel, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := NewRuntimeManager(&ExecRunner{}, filepath.Join(paths.BinDir, "local-runtime-manager"))
	manager.SetOutput(io.Discard, io.Discard)
	state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
	state.OwnerToken = cfg.OwnerToken
	state.OverallStatus = "starting"
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	worker := newCleanupFixture(t, filepath.Join(paths.BinDir, "runtime-worker"), paths.RuntimeRoot, nil)
	registerLocalProcess(paths, "test-worker", worker.cmd.Process.Pid, nil, worker.cmd.Args)
	manager.probeAPI = func(int, time.Duration) bool { return processAlive(worker.cmd.Process.Pid) }
	if err := os.Rename(appRoot, appRoot+".removed"); err != nil {
		t.Fatal(err)
	}
	if err := runRuntimeGuard(ctx, cfg, paths, 123, time.Millisecond, func(int) bool { return false }, manager.Down); err != nil {
		t.Fatal(err)
	}
	if processAlive(worker.cmd.Process.Pid) {
		t.Fatal("owned startup worker survived after the app and cleanup executable were removed")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "user data" {
		t.Fatal("runtime cleanup removed user data")
	}
	if _, err := os.Stat(appRoot); !os.IsNotExist(err) {
		t.Fatal("shutdown recreated the removed application bundle")
	}
}

func TestDesktopLocalProxyDownStopsOnlyItsOwnedProxy(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfigWithOptions(RuntimeConfigOptions{
		Profile: "desktop", OwnerToken: "fixture-proxy-owner", RepoRoot: repo,
		ResourcesRoot: filepath.Join(root, "resources"), RuntimeRoot: filepath.Join(root, "runtime"),
	})
	if err != nil {
		t.Fatal(err)
	}
	proxy := newCleanupFixture(t, paths.LocalProxyBin, paths.RuntimeRoot, nil)
	registerLocalProcess(paths, localProxyProcessName, proxy.cmd.Process.Pid, nil, proxy.cmd.Args)
	connector := newCleanupFixture(t, filepath.Join(paths.BinDir, "lazymind"), paths.RuntimeRoot, nil)
	foreign := newCleanupFixture(t, filepath.Join(root, "foreign", "local-proxy"), filepath.Join(root, "foreign-runtime"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := NewLocalProxyManager(&ExecRunner{}).Down(ctx, cfg, paths); err != nil {
		t.Fatal(err)
	}
	if processAlive(proxy.cmd.Process.Pid) {
		t.Fatal("Desktop local-proxy-down reported success without stopping the owned proxy")
	}
	connector.assertTools(t, 2)
	foreign.assertTools(t, 3)
}

func TestLocalProxyLocalDownKeepsExistingStopScript(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("local", repo)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{t: t}
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, paths.LocalProxyStopScript)
		if cmd.Dir != paths.RepoRoot {
			t.Fatal("local stop script working directory changed")
		}
		return CommandResult{}, nil
	})
	if err := NewLocalProxyManager(runner).Down(context.Background(), cfg, paths); err != nil {
		t.Fatal(err)
	}
	runner.assertCommandCount(1)
}
