//go:build !windows

package agentexec

import (
	"context"
	"os/exec"
)

func commandContext(ctx context.Context, binary string, arguments ...string) (*exec.Cmd, []string, error) {
	return exec.CommandContext(ctx, binary, arguments...), nil, nil
}
