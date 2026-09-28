//go:build darwin

package main

/*
#include <libproc.h>
#include <stdlib.h>
#include <string.h>
#include <sys/proc_info.h>
#include <sys/sysctl.h>

static int lazymind_process_is_guard(pid_t pid) {
	int mib[3] = {CTL_KERN, KERN_PROCARGS2, pid};
	size_t size = 0;
	if (sysctl(mib, 3, NULL, &size, NULL, 0) != 0 || size < sizeof(int)) {
		return 0;
	}
	char *buffer = (char *)malloc(size);
	if (buffer == NULL) {
		return 0;
	}
	if (sysctl(mib, 3, buffer, &size, NULL, 0) != 0) {
		free(buffer);
		return 0;
	}

	int argc = 0;
	memcpy(&argc, buffer, sizeof(argc));
	char *cursor = buffer + sizeof(argc);
	char *end = buffer + size;
	while (cursor < end && *cursor != '\0') {
		cursor++;
	}
	if (cursor < end) {
		cursor++;
	}
	for (int index = 0; index < argc && cursor < end; index++) {
		size_t remaining = (size_t)(end - cursor);
		size_t length = strnlen(cursor, remaining);
		if (length == remaining) {
			break;
		}
		if (strcmp(cursor, "guard") == 0) {
			free(buffer);
			return 1;
		}
		cursor += length + 1;
	}
	free(buffer);
	return 0;
}

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
		if isLocalRuntimeManagerExecutable(exe) && C.lazymind_process_is_guard(C.pid_t(pid)) != 0 {
			continue
		}
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
