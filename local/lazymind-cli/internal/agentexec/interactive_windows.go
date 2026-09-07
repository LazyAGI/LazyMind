//go:build windows

package agentexec

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func OpenInteractiveCommand(binary string) error {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return errors.New("interactive command is required")
	}
	command := interactiveCommand(binary)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open interactive login terminal: %w", err)
	}
	_ = command.Process.Release()
	return nil
}

func interactiveCommand(binary string) *exec.Cmd {
	var command *exec.Cmd
	if extension := strings.ToLower(filepath.Ext(binary)); extension == ".cmd" || extension == ".bat" {
		command = exec.Command("cmd.exe", "/d", "/k", `call "%LAZYMIND_INTERACTIVE_COMMAND%"`)
		command.Env = SafeEnvironment("LAZYMIND_INTERACTIVE_COMMAND=" + windowsShellPath(binary))
	} else {
		command = exec.Command(binary)
		command.Env = SafeEnvironment()
	}
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	return command
}

func windowsShellPath(path string) string {
	if strings.HasPrefix(strings.ToUpper(path), `\\?\UNC\`) {
		return `\\` + path[len(`\\?\UNC\`):]
	}
	if strings.HasPrefix(path, `\\?\`) {
		return path[len(`\\?\`):]
	}
	return path
}
