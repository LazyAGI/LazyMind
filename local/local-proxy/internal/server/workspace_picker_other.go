//go:build !darwin && !windows

package server

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

func pickWorkspaceDirectory(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "zenity", "--file-selection", "--directory", "--title=选择本地工作区").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", errWorkspacePickerCanceled
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
