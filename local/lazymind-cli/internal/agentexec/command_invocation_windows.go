//go:build windows

package agentexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const batchInvocationScript = `$ErrorActionPreference = 'Stop'
$target = $env:LAZYMIND_COMMAND_PATH
$encoded = $env:LAZYMIND_COMMAND_ARGUMENTS
[string[]]$commandArgs = ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encoded)))
& $target @commandArgs
if ($null -eq $LASTEXITCODE) { exit 0 }
exit $LASTEXITCODE`

func commandContext(ctx context.Context, binary string, arguments ...string) (*exec.Cmd, []string, error) {
	extension := strings.ToLower(filepath.Ext(binary))
	if extension != ".cmd" && extension != ".bat" {
		return windowsBackgroundCommand(ctx, binary, arguments...), nil, nil
	}
	powershell, err := windowsPowerShell()
	if err != nil {
		return nil, nil, err
	}
	body, err := json.Marshal(arguments)
	if err != nil {
		return nil, nil, err
	}
	environment := []string{
		"LAZYMIND_COMMAND_PATH=" + binary,
		"LAZYMIND_COMMAND_ARGUMENTS=" + base64.StdEncoding.EncodeToString(body),
	}
	return windowsBackgroundCommand(ctx, powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", batchInvocationScript), environment, nil
}

func windowsPowerShell() (string, error) {
	if candidate := lookPathIn("pwsh.exe", effectiveWindowsPath(), windowsPathExtensions()); candidate != "" {
		return candidate, nil
	}
	if root := strings.TrimSpace(os.Getenv("SystemRoot")); root != "" {
		candidate := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if candidate, err := exec.LookPath("powershell.exe"); err == nil {
		return candidate, nil
	}
	return "", errors.New("Windows PowerShell is unavailable")
}

// These commands communicate over pipes. Interactive login terminals use the
// separate interactiveCommand path and still create their requested console.
func windowsBackgroundCommand(ctx context.Context, binary string, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, binary, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return command
}
