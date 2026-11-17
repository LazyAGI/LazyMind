package codexhost

import (
	"context"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"

	"lazymind/agentconnector/internal/agentexec"
)

// Background probes and dispatchers must not open consoles in the desktop UI.
func backgroundCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Env = agentexec.SafeEnvironment()
	return cmd
}
