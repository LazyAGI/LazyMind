package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winprocess"
	"golang.org/x/sys/windows"
)

const (
	appDataLeaf       = "LazyMind"
	runningExitCode   = 10
	processScanLimit  = 10 * time.Second
	processStopLimit  = 15 * time.Second
	maintenanceLogDir = "Logs"
	registryReadTries = 6
	registryReadDelay = 50 * time.Millisecond
)

type processRegistry struct {
	Processes []processRecord `json:"processes"`
}

type processRecord struct {
	Service string `json:"service"`
	PID     int    `json:"pid"`
	StartID uint64 `json:"startId,omitempty"`
}

type runningProcess struct {
	Service     string
	PID         int
	StartID     uint64
	Executable  string
	MatchReason string
}

type commandOptions struct {
	Command    string
	InstallDir string
}

func main() {
	opts, err := parseCommandOptions(os.Args[1:])
	if err != nil {
		fatalf("%v", err)
	}
	root, err := localAppDataRoot()
	if err != nil {
		fatalf("resolve Local AppData: %v", err)
	}
	switch opts.Command {
	case "check-stopped":
		started := time.Now()
		processes, err := discoverRunningProcesses(root, opts.InstallDir)
		if err != nil {
			logMaintenance(root, "scan failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
			fatalf("scan LazyMind processes: %v", err)
		}
		if len(processes) > 0 {
			summary := processSummary(processes)
			logMaintenance(root, "scan completed in %s; found %d running process(es): %s", time.Since(started).Round(time.Millisecond), len(processes), summary)
			_, _ = fmt.Fprintln(os.Stderr, summary)
			os.Exit(runningExitCode)
		}
		logMaintenance(root, "scan completed in %s; no running LazyMind processes found", time.Since(started).Round(time.Millisecond))
	case "force-stop":
		started := time.Now()
		processes, err := forceStop(root, opts.InstallDir)
		if err != nil {
			logMaintenance(root, "force-stop failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
			fatalf("force-stop LazyMind processes: %v", err)
		}
		summary := processSummary(processes)
		logMaintenance(root, "force-stop completed in %s; stopped %d LazyMind process(es): %s", time.Since(started).Round(time.Millisecond), len(processes), summary)
		if len(processes) > 0 {
			_, _ = fmt.Fprintln(os.Stdout, processSummary(processes))
		}
	case "purge-local-data":
		if err := purgeLocalData(root); err != nil {
			fatalf("purge %s: %v", root, err)
		}
	default:
		fatalf("unsupported command %q", opts.Command)
	}
}

func parseCommandOptions(args []string) (commandOptions, error) {
	if len(args) == 0 {
		return commandOptions{}, errors.New("usage: lazymind-installer-maintenance check-stopped|force-stop|purge-local-data [--install-dir <path>]")
	}
	opts := commandOptions{Command: args[0]}
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--install-dir":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return commandOptions{}, errors.New("--install-dir requires a path")
			}
			opts.InstallDir = filepath.Clean(args[index])
		default:
			return commandOptions{}, fmt.Errorf("unsupported argument %q", args[index])
		}
	}
	return opts, nil
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func localAppDataRoot() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return localAppDataTarget(base)
}

func localAppDataTarget(base string) (string, error) {
	base = filepath.Clean(base)
	if base == "" || base == "." || !filepath.IsAbs(base) {
		return "", fmt.Errorf("invalid Local AppData path %q", base)
	}
	return filepath.Join(base, appDataLeaf), nil
}

func readProcessRegistry(root string) (processRegistry, error) {
	path := filepath.Join(root, "run", "processes.json")
	var lastErr error
	for attempt := 1; attempt <= registryReadTries; attempt++ {
		raw, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return processRegistry{}, nil
			}
			lastErr = err
		} else {
			var registry processRegistry
			if err := json.Unmarshal(raw, &registry); err == nil {
				return registry, nil
			} else {
				lastErr = err
			}
		}
		if attempt < registryReadTries {
			time.Sleep(registryReadDelay)
		}
	}
	return processRegistry{}, fmt.Errorf("read runtime process registry after %d attempts: %w", registryReadTries, lastErr)
}

func checkStopped(root string) error {
	processes, err := discoverRunningProcesses(root, "")
	if err != nil {
		return err
	}
	if len(processes) > 0 {
		return fmt.Errorf("LazyMind is still running: %s", processSummary(processes))
	}
	return nil
}

func discoverRunningProcesses(root, installDir string) ([]runningProcess, error) {
	installDir, err := trustedInstallDir(root, installDir)
	if err != nil {
		return nil, err
	}
	registry, err := readProcessRegistry(root)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), processScanLimit)
	defer cancel()
	inventory, err := winprocess.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	sessionID, err := winprocess.CurrentSession(inventory, os.Getpid())
	if err != nil {
		return nil, err
	}
	excluded := winprocess.ExcludedAncestors(inventory, os.Getpid())
	registered := make(map[int]processRecord, len(registry.Processes))
	executableRoots := compactRoots(root, installDir)
	for _, record := range registry.Processes {
		if record.PID > 0 {
			registered[record.PID] = record
		}
	}
	byPID := make(map[int]runningProcess)
	for _, process := range inventory {
		pid := int(process.ProcessID)
		if pid <= 0 || excluded[pid] {
			continue
		}
		record, registeredPID := registered[pid]
		reason, matched := matchLazyMindProcess(process, record, registeredPID, process.SessionID == sessionID, executableRoots)
		if !matched {
			continue
		}
		executable := strings.TrimSpace(winprocess.Text(process.ExecutablePath))
		service := strings.TrimSpace(record.Service)
		if service == "" {
			if strings.EqualFold(filepath.Base(executable), "LazyMind.exe") {
				service = "desktop"
			} else {
				service = "local-runtime-orphan"
			}
		}
		byPID[pid] = runningProcess{
			Service:     service,
			PID:         pid,
			StartID:     process.StartID,
			Executable:  executable,
			MatchReason: reason,
		}
	}
	processes := make([]runningProcess, 0, len(byPID))
	for _, process := range byPID {
		processes = append(processes, process)
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	return processes, nil
}

func matchLazyMindProcess(process winprocess.Info, record processRecord, registeredPID bool, sameSession bool, executableRoots []string) (string, bool) {
	if registeredPID && record.StartID != 0 && process.StartID == record.StartID {
		return "registered-pid-start-id", true
	}
	executable := strings.TrimSpace(winprocess.Text(process.ExecutablePath))
	if executableMatchesRoots(executable, executableRoots) {
		return "executable-under-owned-root", true
	}
	if sameSession && strings.EqualFold(filepath.Base(executable), "LazyMind.exe") {
		return "desktop-executable-name", true
	}
	return "", false
}

func trustedInstallDir(root, candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", nil
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || !strings.EqualFold(filepath.Base(root), appDataLeaf) {
		return "", fmt.Errorf("invalid LazyMind Local AppData root %q", root)
	}
	// This path is an authorization boundary for terminating every executable
	// below it, not just a convenience validation. The Desktop installer does
	// not allow a custom install directory. If that product policy changes, the
	// helper must authenticate a registered install path instead of trusting a
	// caller-controlled directory merely because its base name is LazyMind.
	expected := filepath.Join(filepath.Dir(root), "Programs", appDataLeaf)
	candidate = filepath.Clean(candidate)
	if !filepath.IsAbs(candidate) || !strings.EqualFold(candidate, expected) {
		return "", fmt.Errorf("untrusted LazyMind install directory %q; expected %q", candidate, expected)
	}
	return expected, nil
}

func compactRoots(values ...string) []string {
	roots := make([]string, 0, len(values))
	for _, value := range values {
		roots = appendUniqueRoot(roots, value)
	}
	return roots
}

func appendUniqueRoot(roots []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return roots
	}
	value = strings.ToLower(filepath.Clean(value))
	if value == "." || !filepath.IsAbs(value) {
		return roots
	}
	for _, root := range roots {
		if root == value {
			return roots
		}
	}
	return append(roots, value)
}

func executableMatchesRoots(executable string, roots []string) bool {
	executable = strings.ToLower(filepath.Clean(strings.TrimSpace(executable)))
	if executable == "" || executable == "." || !filepath.IsAbs(executable) {
		return false
	}
	for _, root := range roots {
		if executable == root || strings.HasPrefix(executable, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func processSummary(processes []runningProcess) string {
	parts := make([]string, 0, len(processes))
	for _, process := range processes {
		name := strings.TrimSpace(process.Service)
		if name == "" {
			name = filepath.Base(process.Executable)
		}
		if name == "" {
			name = "unknown"
		}
		executable := filepath.Base(process.Executable)
		if executable == "" || executable == "." {
			executable = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s(pid=%d, exe=%s, reason=%s)", name, process.PID, executable, process.MatchReason))
	}
	return strings.Join(parts, ", ")
}

func forceStop(root, installDir string) ([]runningProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), processStopLimit)
	defer cancel()
	stopped := make([]runningProcess, 0)
	seen := make(map[int]bool)
	deadline := time.Now().Add(processStopLimit)
	for {
		processes, err := discoverRunningProcesses(root, installDir)
		if err != nil {
			return stopped, err
		}
		if len(processes) == 0 {
			_ = os.Remove(filepath.Join(root, "run", "processes.json"))
			return stopped, nil
		}
		if time.Now().After(deadline) {
			return stopped, fmt.Errorf("processes still running after %s: %s", processStopLimit, processSummary(processes))
		}
		for _, process := range processes {
			if !seen[process.PID] {
				stopped = append(stopped, process)
				seen[process.PID] = true
			}
			if err := winprocess.ForceKillTree(ctx, process.PID, process.StartID); errors.Is(err, winprocess.ErrProcessIdentityChanged) {
				continue
			} else if err != nil {
				return stopped, fmt.Errorf("stop %s pid %d: %w", process.Service, process.PID, err)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func processAlive(pid uint32) bool {
	return winprocess.Alive(int(pid))
}

func processStartIdentity(pid uint32) uint64 {
	return winprocess.StartIdentity(int(pid))
}

func logMaintenance(root, format string, args ...any) {
	path := filepath.Join(root, maintenanceLogDir, "desktop", "installer-maintenance.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func purgeLocalData(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(target))
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to purge a reparse-point data root")
	}
	parent := filepath.Dir(target)
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	tombstone := fmt.Sprintf(".%s-uninstall-%d-%d", appDataLeaf, os.Getpid(), time.Now().UnixNano())
	if err := root.Rename(appDataLeaf, tombstone); err != nil {
		return fmt.Errorf("quarantine data root: %w", err)
	}
	if err := root.RemoveAll(tombstone); err != nil {
		if restoreErr := root.Rename(tombstone, appDataLeaf); restoreErr != nil {
			return fmt.Errorf("delete quarantined data: %w; restore also failed: %v", err, restoreErr)
		}
		return fmt.Errorf("delete quarantined data: %w", err)
	}
	return nil
}
