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
		cmdline := splitProcCmdline(cmdlineRaw)
		cmdlineText := strings.Join(cmdline, " ")
		if !processTextMatchesRuntime(paths, exe, cmdlineText) {
			continue
		}
		records = append(records, LocalProcessRecord{
			Service:     inferServiceFromProcessText(paths, exe+" "+cmdlineText),
			PID:         pid,
			PGID:        processGroupID(pid),
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
			Command:     cmdline,
		})
	}
	return records, nil
}

func splitProcCmdline(raw []byte) []string {
	trimmed := strings.TrimSuffix(string(raw), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}
