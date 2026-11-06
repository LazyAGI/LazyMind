//go:build darwin || linux

package main

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
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
