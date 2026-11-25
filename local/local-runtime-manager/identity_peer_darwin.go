package main

/*
#include <libproc.h>
*/
import "C"

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func identityPeerPID(fd uintptr) (int, error) {
	return unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}

func identityPeerParent(pid int) (int, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	if info.Proc.P_stat == darwinZombieProcessState {
		return 0, os.ErrInvalid
	}
	return int(info.Eproc.Ppid), nil
}

func identityPeerImagePath(pid int) (string, error) {
	var path [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	n, err := C.proc_pidpath(C.int(pid), unsafe.Pointer(&path[0]), C.uint32_t(len(path)))
	if n <= 0 {
		if err != nil {
			return "", err
		}
		return "", os.ErrInvalid
	}
	return C.GoString(&path[0]), nil
}
