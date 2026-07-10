//go:build darwin

package main

/*
#include <libproc.h>
#include <stdlib.h>
*/
import "C"

import (
	"os"
	"strings"
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
		if !processTextMatchesRuntime(paths, exe, "") {
			continue
		}
		records = append(records, LocalProcessRecord{
			Service:     inferServiceFromProcessText(paths, exe),
			PID:         pid,
			PGID:        processGroupID(pid),
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
			Command:     []string{strings.TrimSpace(exe)},
		})
	}
	return records, nil
}
