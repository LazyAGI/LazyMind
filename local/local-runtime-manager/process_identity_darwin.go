//go:build darwin

package main

import "golang.org/x/sys/unix"

const darwinZombieProcessState = 5 // SZOMB in sys/proc.h.

func processStartIdentity(pid int) uint64 {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info.Proc.P_stat == darwinZombieProcessState {
		return 0
	}
	return uint64(info.Proc.P_starttime.Sec)*1_000_000 + uint64(info.Proc.P_starttime.Usec)
}
