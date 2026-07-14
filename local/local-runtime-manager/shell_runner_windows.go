//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func processComposeShellCommand(ctx context.Context, executable, command string) (*exec.Cmd, error) {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "internal" {
		return nil, fmt.Errorf("unsupported process-compose shell command: %q", command)
	}
	cmd := exec.CommandContext(ctx, executable, fields...)
	configureChildProcess(cmd, false)
	return cmd, nil
}

func runProcessComposeShell(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("shell requires exactly one command argument")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve shell executable: %w", err)
	}
	cmd, err := processComposeShellCommand(ctx, executable, args[0])
	if err != nil {
		return err
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
