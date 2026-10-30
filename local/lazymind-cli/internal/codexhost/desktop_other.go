//go:build !darwin && !windows

package codexhost

import (
	"context"
	"errors"
)

func launchDesktop(context.Context, string, int) error {
	return errors.New("Codex Desktop CDP launch is not supported on this platform")
}

func openDesktopThread(context.Context, string, string) error {
	return errors.New("Codex conversation navigation is not supported on this platform")
}
