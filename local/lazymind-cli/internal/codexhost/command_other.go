//go:build !windows

package codexhost

import (
	"context"
	"os/exec"
)

func backgroundCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
