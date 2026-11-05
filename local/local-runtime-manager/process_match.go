package main

import (
	"path/filepath"
	"strings"
)

const desktopOwnerPIDEnvVar = "LAZYMIND_DESKTOP_OWNER_PID"

func processTextMatchesRuntime(paths RuntimePaths, parts ...string) bool {
	text := strings.Join(parts, " ")
	root := filepath.Clean(paths.RuntimeRoot)
	if root == "." || root == string(filepath.Separator) {
		return false
	}
	// Executable, source and dependency directories can be shared by several
	// deployments. Only a reference to this runtime's private directory proves
	// ownership; a sibling path with the same prefix does not.
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], root)
		if index < 0 {
			break
		}
		index += offset
		end := index + len(root)
		before := index == 0 || strings.ContainsRune(" \t\n\x00\"'=", rune(text[index-1]))
		after := end == len(text) || strings.ContainsRune("/\\ \t\n\x00\"'", rune(text[end]))
		if before && after {
			return true
		}
		offset = end
	}
	return false
}

func processEnvironmentMatchesRuntime(paths RuntimePaths, executable string, args, environment []string) bool {
	for _, value := range environment {
		if value == runtimeProfileEnvVar+"=bridge" {
			return false
		}
	}
	if isAssistantBridgeCommand(executable, args) {
		return false
	}
	for _, value := range environment {
		if root, ok := strings.CutPrefix(value, runtimeRootEnvVar+"="); ok {
			return root != "" && filepath.Clean(root) == filepath.Clean(paths.RuntimeRoot)
		}
	}
	for i, arg := range args {
		if arg == "--runtime-root" && i+1 < len(args) {
			return filepath.Clean(args[i+1]) == filepath.Clean(paths.RuntimeRoot)
		}
		if root, ok := strings.CutPrefix(arg, "--runtime-root="); ok {
			return filepath.Clean(root) == filepath.Clean(paths.RuntimeRoot)
		}
	}
	return processTextMatchesRuntime(paths, strings.Join(args, " "))
}

func isAssistantBridgeCommand(executable string, args []string) bool {
	if !strings.HasPrefix(strings.ToLower(filepath.Base(executable)), "lazymind") {
		return false
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "assistant" && args[i+1] == "serve" {
			return true
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
