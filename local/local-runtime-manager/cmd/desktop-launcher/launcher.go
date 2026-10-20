package main

import (
	"path/filepath"
	"strings"
)

const (
	runAsNodeVariable = "ELECTRON_RUN_AS_NODE"
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
		result = append(result, entry)
	}
	return result, removed
}
