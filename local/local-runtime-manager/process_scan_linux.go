//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func scanLocalRuntimeProcesses(paths RuntimePaths) ([]LocalProcessRecord, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	records := []LocalProcessRecord{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		root := filepath.Join("/proc", entry.Name())
		exe, _ := os.Readlink(filepath.Join(root, "exe"))
		cmdlineRaw, _ := os.ReadFile(filepath.Join(root, "cmdline"))
		cmdline := strings.ReplaceAll(string(cmdlineRaw), "\x00", " ")
		if !processTextMatchesRuntime(paths, exe, cmdline) {
			continue
		}
		records = append(records, LocalProcessRecord{
			Service:     inferServiceFromProcessText(paths, exe+" "+cmdline),
			PID:         pid,
			PGID:        processGroupID(pid),
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
			Command:     splitCommandLine(cmdline),
		})
	}
	return records, nil
}

func splitCommandLine(raw string) []string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil
	}
	return fields
}
