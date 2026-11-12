package desktopupdate

import (
	"errors"
	"golang.org/x/sys/unix"
)

func processStamp(pid int) (uint64, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// sysctl reports EIO for a missing record. Only an independent ESRCH
		// confirms absence; permissions/query failures must not mean stopped.
		if errors.Is(err, unix.ESRCH) || errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
			return 0, nil
		}
		return 0, err
	}
	stamp := info.Proc.P_starttime
	return uint64(stamp.Sec)*1000000 + uint64(stamp.Usec), nil
}
