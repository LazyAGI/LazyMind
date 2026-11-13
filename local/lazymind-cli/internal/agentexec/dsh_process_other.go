//go:build !darwin && !windows

package agentexec

import (
	"context"
	"errors"
)

func platformDSHDesktopRunning(context.Context, string) (bool, error) {
	return false, errors.New("DSH Desktop process inspection is unsupported")
}
