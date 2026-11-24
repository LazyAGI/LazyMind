//go:build !windows

package codexhost

import (
	"context"
	"os/exec"

	"lazymind/agentconnector/internal/agentexec"
)

func backgroundCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	// Use the same environment for preflight and the actual dispatcher. Ambient
	// Node preloads must not turn a successful version check into a startup failure.
	cmd.Env = agentexec.SafeEnvironment()
	return cmd
}
