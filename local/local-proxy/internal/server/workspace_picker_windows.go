//go:build windows

package server

import (
	"context"
	"os/exec"
	"strings"
)

func pickWorkspaceDirectory(ctx context.Context) (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description = '选择本地工作区'; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($dialog.SelectedPath) } else { exit 2 }`
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script).Output()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
		return "", errWorkspacePickerCanceled
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
