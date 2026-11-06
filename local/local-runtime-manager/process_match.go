package main

import (
	"path/filepath"
	"strings"
)

const desktopOwnerPIDEnvVar = "LAZYMIND_DESKTOP_OWNER_PID"

func processTextMatchesRuntime(paths RuntimePaths, parts ...string) bool {
	if len(parts) > 0 && isConnectorExecutable(parts[0]) {
		return false
	}
	text := strings.Join(parts, " ")
	repo := filepath.Clean(paths.RepoRoot)
	buildRoot := filepath.Clean(paths.BuildRoot)
	runtimeRoot := filepath.Clean(paths.RuntimeRoot)
	resourcesRoot := filepath.Clean(paths.ResourcesRoot)
	if buildRoot != "." && strings.Contains(text, buildRoot) {
		return true
	}
	if strings.Contains(text, runtimeRoot) {
		return true
	}
	if resourcesRoot != "." && strings.Contains(text, resourcesRoot) {
		return true
	}
	if strings.Contains(text, repo) {
		for _, marker := range []string{
			"local-runtime-manager",
			"process-compose",
			"local-proxy",
			"scan-control-plane",
			"file-watcher",
			"auth-service",
			"channel-gateway",
			"backend/core",
			"algorithm/lazymind",
			"algorithm/lazyllm",
		} {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

func isLocalRuntimeManagerExecutable(executable string) bool {
	base := strings.ToLower(filepath.Base(executable))
	return strings.TrimSuffix(base, filepath.Ext(base)) == "local-runtime-manager"
}

func isRuntimeGuardCommand(executable string, args []string) bool {
	if !isLocalRuntimeManagerExecutable(executable) {
		return false
	}
	for _, arg := range args {
		if arg == "guard" {
			return true
		}
	}
	return false
}
