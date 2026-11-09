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
		stat, err := os.ReadFile(filepath.Join(root, "stat"))
		if err != nil {
			continue
		}
		parent, group, startID := parseLinuxProcessStat(string(stat))
		environ, _ := os.ReadFile(filepath.Join(root, "environ"))
		records = append(records, LocalProcessRecord{
			PID: pid, ParentPID: parent, PGID: group, StartID: startID,
			Executable: strings.TrimSuffix(exe, " (deleted)"), Command: cmdline,
			OwnerRoot: processOwnerFromEnvironment(environ),
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

func processStartIdentity(pid int) uint64 {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	_, _, startID := parseLinuxProcessStat(string(raw))
	return startID
}

func parseLinuxProcessStat(raw string) (parent, group int, startID uint64) {
	// comm may itself contain spaces and parentheses. Fields after its final
	// closing parenthesis start at field 3; starttime is field 22.
	end := strings.LastIndexByte(raw, ')')
	if end < 0 {
		return
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) < 20 {
		return
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return
	}
	parent, _ = strconv.Atoi(fields[1])
	group, _ = strconv.Atoi(fields[2])
	startID, _ = strconv.ParseUint(fields[19], 10, 64)
	return
}
