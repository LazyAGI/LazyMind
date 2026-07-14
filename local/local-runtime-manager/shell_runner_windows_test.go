//go:build windows

package main

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProcessComposeShellCommandIsHidden(t *testing.T) {
	cmd, err := processComposeShellCommand(
		context.Background(),
		`C:\Program Files\LazyMind\local-runtime-manager.exe`,
		"internal core-run",
	)
	if err != nil {
		t.Fatalf("process-compose shell command: %v", err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("process-compose shell command must hide its window")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.Path != `C:\Program Files\LazyMind\local-runtime-manager.exe` {
		t.Fatalf("command path = %q", cmd.Path)
	}
}
