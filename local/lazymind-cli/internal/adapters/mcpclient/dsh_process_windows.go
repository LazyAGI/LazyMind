//go:build windows

package mcpclient

import (
	"errors"

	"golang.org/x/sys/windows"
)

func dshProcessExited(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// Invalid PID proves absence; access denied does not prove termination.
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	state, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && state == windows.WAIT_OBJECT_0
}
