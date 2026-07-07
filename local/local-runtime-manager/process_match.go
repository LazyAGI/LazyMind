package main

import (
	"path/filepath"
	"strings"
)

func processTextMatchesRuntime(paths RuntimePaths, parts ...string) bool {
	text := strings.Join(parts, " ")
	repo := filepath.Clean(paths.RepoRoot)
	runtimeRoot := filepath.Clean(paths.RuntimeRoot)
	if strings.Contains(text, runtimeRoot) {
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

func inferServiceFromProcessText(paths RuntimePaths, text string) string {
	candidates := []string{
		processComposeServiceName,
		localProxyProcessName,
		authServiceProcessName,
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
