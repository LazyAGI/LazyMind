//go:build darwin

package server

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

func pickWorkspaceDirectory(ctx context.Context) (string, error) {
	script := `set selectedFolder to choose folder with prompt "选择本地工作区"
POSIX path of selectedFolder`
	output, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", errWorkspacePickerCanceled
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
