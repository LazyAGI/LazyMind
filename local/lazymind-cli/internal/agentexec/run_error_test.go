package agentexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunPreservesProcessErrorAndDiagnostic(t *testing.T) {
	t.Setenv("SHELL", "")
	name, script := "failure", "#!/bin/sh\necho credential-store-unreadable >&2\nexit 7\n"
	if runtime.GOOS == "windows" {
		name, script = "failure.cmd", "@echo off\r\necho credential-store-unreadable 1>&2\r\nexit /b 7\r\n"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), path)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "credential-store-unreadable") {
		t.Fatalf("process cause/diagnostic lost: %T %v", err, err)
	}
}

func TestRunPreservesCancellation(t *testing.T) {
	t.Setenv("SHELL", "")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Run(ctx, binary)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %T %v", err, err)
	}
}
