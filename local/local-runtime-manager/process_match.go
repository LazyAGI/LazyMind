package main

import (
	"path/filepath"
	"strings"
)

const desktopOwnerPIDEnvVar = "LAZYMIND_DESKTOP_OWNER_PID"

func processTextMatchesRuntime(paths RuntimePaths, parts ...string) bool {
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

func inferServiceFromProcessText(paths RuntimePaths, text string) string {
	candidates := []string{
		processComposeServiceName,
		sqliteServerProcessName,
		localProxyProcessName,
		authServiceProcessName,
		channelGatewayProcessName,
		coreProcessName,
		scanControlPlaneProcessName,
		fileWatcherProcessName,
		frontendProcessName,
		milvusLiteProcessName,
		docServerProcessName,
		processorServerProcessName,
		processorWorkerProcessName,
		algoProcessName,
		chatProcessName,
		evoProcessName,
	}
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return candidate
		}
	}
	if strings.Contains(text, paths.CaddyBin) || strings.Contains(text, "caddy") {
		return frontendProcessName
	}
	if strings.Contains(text, paths.CoreBin) {
		return coreProcessName
	}
	if strings.Contains(text, paths.ScanControlPlaneBin) {
		return scanControlPlaneProcessName
	}
	if strings.Contains(text, paths.FileWatcherBin) {
		return fileWatcherProcessName
	}
	return "local-runtime-orphan"
}
