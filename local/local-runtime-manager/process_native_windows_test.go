//go:build windows

package main

import (
	"errors"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

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
