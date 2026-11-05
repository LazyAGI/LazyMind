//go:build darwin

package main

/*
#include <libproc.h>
#include <stdlib.h>
#include <string.h>
#include <sys/proc_info.h>
#include <sys/sysctl.h>

static char *lazymind_process_arguments(pid_t pid, size_t *size) {
 int mib[3] = {CTL_KERN, KERN_PROCARGS2, pid};
 int argmax = 0;
 size_t length = sizeof(argmax);
 int limit_mib[2] = {CTL_KERN, KERN_ARGMAX};
 if (sysctl(limit_mib, 2, &argmax, &length, NULL, 0) != 0 || argmax < sizeof(int)) return NULL;
 *size = (size_t)argmax;
 char *buffer = (char *)malloc(*size);
 if (buffer == NULL) return NULL;
 if (sysctl(mib, 3, buffer, size, NULL, 0) != 0) { free(buffer); return NULL; }
 return buffer;
}

*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"os"
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
		args, environment := darwinProcessArguments(pid)
		if isRuntimeGuardCommand(exe, args) {
			continue
		}
		if !processEnvironmentMatchesRuntime(paths, exe, args, environment) {
			continue
		}
		records = append(records, LocalProcessRecord{
			Service:     inferServiceFromProcessText(paths, exe),
			PID:         pid,
			PGID:        processGroupID(pid),
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
			Command:     args,
		})
	}
	return records, nil
}

func darwinProcessArguments(pid int) ([]string, []string) {
	var size C.size_t
	raw := C.lazymind_process_arguments(C.pid_t(pid), &size)
	if raw == nil {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(raw))
	return parseDarwinProcessArguments(C.GoBytes(unsafe.Pointer(raw), C.int(size)))
}

func parseDarwinProcessArguments(raw []byte) ([]string, []string) {
	if len(raw) < 4 {
		return nil, nil
	}
	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	raw = raw[4:]
	end := bytes.IndexByte(raw, 0)
	if end < 0 {
		return nil, nil
	}
	raw = bytes.TrimLeft(raw[end+1:], "\x00")
	args := []string{}
	for i := 0; i < argc; i++ {
		end = bytes.IndexByte(raw, 0)
		if end < 0 {
			return nil, nil
		}
		args = append(args, string(raw[:end]))
		raw = raw[end+1:]
	}
	environment := []string{}
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) > 0 {
			environment = append(environment, string(entry))
		}
	}
	return args, environment
}
