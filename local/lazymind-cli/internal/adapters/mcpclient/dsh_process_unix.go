//go:build !windows

package mcpclient

import (
	"errors"

	"golang.org/x/sys/unix"
)

func dshProcessExited(pid int) bool {
	return errors.Is(unix.Kill(pid, 0), unix.ESRCH)
}
