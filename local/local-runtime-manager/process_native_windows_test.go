//go:build windows

package main

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureChildProcessHidesBackgroundWindow(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "exit 0")
	configureChildProcess(cmd, false)
	if cmd.SysProcAttr == nil {
		t.Fatal("expected Windows process attributes")
	}
	wantFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	if cmd.SysProcAttr.CreationFlags&wantFlags != wantFlags {
		t.Fatalf("creation flags = %#x, want %#x", cmd.SysProcAttr.CreationFlags, wantFlags)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("expected background child window to be hidden")
	}
}

func TestExecRunnerHidesBackgroundWindow(t *testing.T) {
	cmd := execCommand(context.Background(), Command{Name: "powershell.exe", Args: []string{"-NoProfile", "-Command", "exit 0"}})
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("expected ExecRunner command window to be hidden")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("ExecRunner creation flags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

func TestAttachProcessJobAllowsNestedJobAssignmentFailure(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	original := assignProcessToJobObject
	assignProcessToJobObject = func(windows.Handle, windows.Handle) error {
		return errors.New("nested job assignment denied")
	}
	t.Cleanup(func() { assignProcessToJobObject = original })

	cleanup, err := attachProcessJob(RuntimePaths{RuntimeRoot: t.TempDir()}, "nested-job-test", cmd.Process)
	if err != nil {
		t.Fatalf("nested job assignment should be non-fatal: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected no-op cleanup function")
	}
	cleanup()
	if !processAlive(cmd.Process.Pid) {
		t.Fatal("child process should remain alive after containment fallback")
	}
}
