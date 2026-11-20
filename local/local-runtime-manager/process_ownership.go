package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Workers inherit this label, including Python resource trackers that outlive
// their parent. Connector processes have a separate lifecycle and are excluded.
const runtimeProcessOwnerEnvVar = "LAZYMIND_RUNTIME_PROCESS_OWNER"

func isConnectorExecutable(executable string) bool {
	base := filepath.Base(strings.ReplaceAll(executable, "\\", "/"))
	if runtime.GOOS == "windows" || strings.Contains(executable, "\\") {
		return strings.EqualFold(base, "lazymind") || strings.EqualFold(base, "lazymind.exe")
	}
	return base == "lazymind" || base == "lazymind.exe"
}

func sameProcessPath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	if left == right {
		return true
	}
	// macOS reports /private/var for programs launched through /var.
	l, lerr := filepath.EvalSymlinks(left)
	r, rerr := filepath.EvalSymlinks(right)
	return lerr == nil && rerr == nil && l == r
}

func processOwnerFromEnvironment(raw []byte) string {
	for _, value := range strings.Split(string(raw), "\x00") {
		if owner, ok := strings.CutPrefix(value, runtimeProcessOwnerEnvVar+"="); ok {
			return owner
		}
	}
	return ""
}

// Pre-label runtimes can be identified by an exact service executable or an
// internal service-run command, never just their containing directory.
func legacyRuntimeService(paths RuntimePaths, process LocalProcessRecord) string {
	for _, item := range []struct{ path, service string }{
		{paths.ProcessComposeBin, processComposeServiceName},
		{paths.CoreBin, coreProcessName},
		{paths.LocalProxyBin, localProxyProcessName},
		{paths.ScanControlPlaneBin, scanControlPlaneProcessName},
		{paths.FileWatcherBin, fileWatcherProcessName},
		{paths.CaddyBin, frontendProcessName},
	} {
		if sameProcessPath(process.Executable, item.path) {
			return item.service
		}
	}
	manager := "local-runtime-manager"
	if runtime.GOOS == "windows" {
		manager += ".exe"
	}
	if !sameProcessPath(process.Executable, filepath.Join(paths.BinDir, manager)) {
		return ""
	}
	args := process.Command
	if len(args) < 3 || args[1] != "internal" {
		return ""
	}
	for _, service := range []string{
		sqliteServerProcessName, localProxyProcessName, authServiceProcessName,
		channelGatewayProcessName, coreProcessName, scanControlPlaneProcessName,
		fileWatcherProcessName, frontendProcessName, milvusLiteProcessName,
		processorServerProcessName, processorWorkerProcessName, algoProcessName,
		docServerProcessName, chatProcessName, evoProcessName,
	} {
		if args[2] == service+"-run" {
			return service
		}
	}
	return ""
}

func hasConnectorAncestor(pid, desktopOwner int, processes map[int]LocalProcessRecord) bool {
	seen := map[int]bool{}
	for pid > 0 && pid != desktopOwner && !seen[pid] {
		seen[pid] = true
		process := processes[pid]
		if isConnectorExecutable(process.Executable) {
			return true
		}
		pid = process.ParentPID
	}
	return false
}

// A process inventory is not an ownership declaration. A cleanup target must
// have a verified registration, inherited owner label, exact legacy service
// identity, or a parent that satisfies one of those conditions.
func ownedRuntimeProcesses(paths RuntimePaths, registered, inventory []LocalProcessRecord, skipped func(LocalProcessRecord)) []LocalProcessRecord {
	byPID := make(map[int]LocalProcessRecord, len(inventory))
	for _, process := range inventory {
		byPID[process.PID] = process
	}
	desktopOwner, _ := strconv.Atoi(os.Getenv(desktopOwnerPIDEnvVar))
	protected := map[int]bool{os.Getpid(): true}
	if desktopOwner > 0 {
		protected[desktopOwner] = true
	}
	for pid := os.Getppid(); pid > 0 && !protected[pid]; pid = byPID[pid].ParentPID {
		protected[pid] = true
	}
	for _, process := range inventory {
		if hasConnectorAncestor(process.PID, desktopOwner, byPID) || isRuntimeGuardCommand(process.Executable, process.Command) {
			protected[process.PID] = true
		}
		if process.OwnerRoot != "" && !sameProcessPath(process.OwnerRoot, paths.RuntimeRoot) {
			protected[process.PID] = true
		}
	}
	// A stale registry entry must not be rescued by a pidfile or path fallback.
	for _, record := range registered {
		if process, ok := byPID[record.PID]; ok && localProcessBelongsToRuntime(record, paths) && record.StartID != 0 && record.StartID != process.StartID {
			protected[record.PID] = true
		}
	}
	owned := make(map[int]LocalProcessRecord)
	add := func(process LocalProcessRecord, service string) {
		if process.PID <= 0 || protected[process.PID] || process.StartID == 0 {
			return
		}
		if service == "" {
			service = "local-runtime-orphan"
		}
		process.Service = service
		process.RepoRoot, process.RuntimeRoot = paths.RepoRoot, paths.RuntimeRoot
		owned[process.PID] = process
	}
	for _, record := range registered {
		process, exists := byPID[record.PID]
		if !exists || !localProcessBelongsToRuntime(record, paths) {
			continue
		}
		process.Ports, process.StartedAt = record.Ports, record.StartedAt
		if len(process.Command) <= 1 && len(record.Command) > 0 {
			process.Command = record.Command
		}
		if record.StartID != 0 && record.StartID == process.StartID {
			add(process, record.Service)
		} else if record.StartID == 0 {
			if service := legacyRuntimeService(paths, process); service != "" {
				add(process, service)
			}
		}
	}
	for _, process := range inventory {
		if _, exists := owned[process.PID]; exists {
			continue
		}
		if process.OwnerRoot != "" {
			if sameProcessPath(process.OwnerRoot, paths.RuntimeRoot) {
				add(process, legacyRuntimeService(paths, process))
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, process := range inventory {
			if _, exists := owned[process.PID]; exists || protected[process.PID] || process.StartID == 0 {
				continue
			}
			parent, exists := owned[process.ParentPID]
			if exists && process.StartID >= parent.StartID && (process.OwnerRoot == "" || sameProcessPath(process.OwnerRoot, paths.RuntimeRoot)) {
				add(process, "")
				changed = true
			}
		}
	}
	result := make([]LocalProcessRecord, 0, len(owned))
	for _, process := range owned {
		result = append(result, process)
	}
	if skipped != nil {
		for _, process := range inventory {
			if _, ok := owned[process.PID]; !ok && process.PID != os.Getpid() && (processTextMatchesRuntime(paths, process.Executable) || legacyRuntimeService(paths, process) != "") {
				skipped(process)
			}
		}
	}
	return result
}
