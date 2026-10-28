//go:build !darwin && !windows

package desktopsetup

import (
	"context"
	"errors"
)

func Running(context.Context, string) (bool, error) {
	return false, errors.New("desktop process inspection is unsupported")
}
func ListenerMatches(context.Context, string, int) bool { return false }
