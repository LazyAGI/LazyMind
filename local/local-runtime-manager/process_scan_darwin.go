//go:build darwin

package main

/*
#include <libproc.h>
#include <stdlib.h>
#include <string.h>
#include <sys/proc_info.h>
#include <sys/sysctl.h>

static char* lazymind_process_args(pid_t pid, int* length) {
    int mib[3] = {CTL_KERN, KERN_PROCARGS2, pid};
    size_t size = 0;
    if (sysctl(mib, 3, NULL, &size, NULL, 0) != 0 || size < sizeof(int)) return NULL;
    char* buffer = (char*)malloc(size);
    if (buffer == NULL) return NULL;
    if (sysctl(mib, 3, buffer, &size, NULL, 0) != 0) { free(buffer); return NULL; }
    *length = (int)size;
    return buffer;
}

*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
	"unsafe"
)

func scanLocalRuntimeProcesses(paths RuntimePaths) ([]LocalProcessRecord, error) {
	size := C.proc_listpids(C.PROC_ALL_PIDS, 0, nil, 0)
	if size <= 0 {
		return nil, nil
	}
	pidCount := int(size) / C.sizeof_int
	if pidCount == 0 {
		return nil, nil
	}
	pids := make([]C.int, pidCount)
	size = C.proc_listpids(C.PROC_ALL_PIDS, 0, unsafe.Pointer(&pids[0]), size)
	if size <= 0 {
		return nil, nil
	}
	records := []LocalProcessRecord{}
	for _, rawPID := range pids[:int(size)/C.sizeof_int] {
		pid := int(rawPID)
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		pathBuffer := make([]C.char, C.PROC_PIDPATHINFO_MAXSIZE)
		ret := C.proc_pidpath(C.int(pid), unsafe.Pointer(&pathBuffer[0]), C.uint32_t(len(pathBuffer)))
		if ret <= 0 {
			continue
		}
		exe := C.GoString(&pathBuffer[0])
		info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || info.Proc.P_stat == darwinZombieProcessState {
			continue
		}
		var length C.int
		raw := C.lazymind_process_args(C.pid_t(pid), &length)
		var args []string
		var owner string
		if raw != nil {
			args, owner = parseDarwinProcessArgs(C.GoBytes(unsafe.Pointer(raw), length))
			C.free(unsafe.Pointer(raw))
		}
		records = append(records, LocalProcessRecord{
			PID: pid, ParentPID: int(info.Eproc.Ppid), PGID: int(info.Eproc.Pgid),
			Executable: exe, Command: args, OwnerRoot: owner,
			StartID: uint64(info.Proc.P_starttime.Sec)*1_000_000 + uint64(info.Proc.P_starttime.Usec),
		})
	}
	return records, nil
}

// KERN_PROCARGS2 contains argc, executable path, padding, argv, then environ.
// Do not search argv for the ownership label: an argument is not an environment.
func parseDarwinProcessArgs(raw []byte) ([]string, string) {
	if len(raw) < 4 {
		return nil, ""
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	if argc <= 0 || argc > len(raw) {
		return nil, ""
	}
	raw = raw[4:]
	end := bytes.IndexByte(raw, 0)
	if end < 0 {
		return nil, ""
	}
	raw = raw[end+1:]
	for len(raw) > 0 && raw[0] == 0 {
		raw = raw[1:]
	}
	args := make([]string, 0, argc)
	for i := 0; i < argc; i++ {
		end = bytes.IndexByte(raw, 0)
		if end < 0 {
			return nil, ""
		}
		args = append(args, string(raw[:end]))
		raw = raw[end+1:]
	}
	return args, processOwnerFromEnvironment(raw)
}
