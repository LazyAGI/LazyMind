//go:build !darwin && !windows

package workbuddyhost

import (
	"context"
	"errors"
)

func launchDesktop(context.Context, string, int) error {
	return errors.New("WorkBuddy desktop startup is supported on macOS and Windows")
}
