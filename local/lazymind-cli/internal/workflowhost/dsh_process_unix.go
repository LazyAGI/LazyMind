//go:build !windows

package workflowhost

import (
	"errors"

	"golang.org/x/sys/unix"
)

func DSHProcessExited(pid int) bool {
	return errors.Is(unix.Kill(pid, 0), unix.ESRCH)
}
