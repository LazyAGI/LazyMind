//go:build darwin

package agentexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var startInteractiveTerminal = func(script string) error {
	return exec.Command("/usr/bin/open", "-a", "Terminal", script).Run()
}

func OpenInteractiveCommand(binary string) error {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return errors.New("interactive command is required")
	}
	file, err := os.CreateTemp("", "lazymind-agent-login-*.command")
	if err != nil {
		return fmt.Errorf("create interactive login command: %w", err)
	}
	path := file.Name()
	script := "#!/bin/sh\nrm -f -- \"$0\"\nexec " + quoteShellArgument(binary) + "\n"
	if _, err = file.WriteString(script); err == nil {
		err = file.Chmod(0o700)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("prepare interactive login command: %w", err)
	}
	if err = startInteractiveTerminal(path); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("open interactive login terminal: %w", err)
	}
	return nil
}

func quoteShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
