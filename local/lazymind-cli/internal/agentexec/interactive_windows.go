//go:build windows

package agentexec

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func OpenInteractiveCommand(binary string) error {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return errors.New("interactive command is required")
	}
	command := exec.Command(
		"cmd.exe", "/d", "/s", "/c", `start "" "%LAZYMIND_INTERACTIVE_COMMAND%"`,
	)
	command.Env = SafeEnvironment("LAZYMIND_INTERACTIVE_COMMAND=" + binary)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open interactive login terminal: %w", err)
	}
	return nil
}
