package main

import (
	"path/filepath"
	"runtime"
	"strings"
)

const (
	runAsNodeVariable       = "ELECTRON_RUN_AS_NODE"
	desktopDataRootVariable = "LAZYMIND_DESKTOP_DATA_ROOT"
)

func electronExecutablePath(launcherPath string, executableName string) string {
	return filepath.Join(filepath.Dir(launcherPath), executableName)
}

func sanitizedEnvironment(environment []string) ([]string, bool) {
	result := make([]string, 0, len(environment))
	removed := false
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(strings.TrimSpace(name), runAsNodeVariable) {
			removed = true
			continue
		}
		if found && runtime.GOOS == "windows" && strings.EqualFold(strings.TrimSpace(name), desktopDataRootVariable) {
			continue
		}
		result = append(result, entry)
	}
	return result, removed
}
