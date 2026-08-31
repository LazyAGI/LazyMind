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
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winfile"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winprocess"
	"golang.org/x/sys/windows"
)

const (
	appDataLeaf       = "LazyMind"
	runningExitCode   = 10
	processScanLimit  = 10 * time.Second
	processStopLimit  = 15 * time.Second
	purgeRetryLimit   = 2 * time.Minute
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
	Command               string
	InstallDir            string
	TempDir               string
	MinimumFreeSpaceMB    uint64
	MaximumRelativeLength int
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
	case "preflight":
		if err := installerPreflight(opts); err != nil {
			logMaintenance(root, "installer preflight failed: %v", err)
			fatalf("installer preflight: %v", err)
		}
		logMaintenance(root, "installer preflight passed for install-dir=%q temp-dir=%q", opts.InstallDir, opts.TempDir)
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
		started := time.Now()
		processes, err := forceStop(root, opts.InstallDir)
		if err != nil {
			logMaintenance(root, "pre-purge force-stop failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
			fatalf("stop LazyMind before purge: %v", err)
		}
		if len(processes) > 0 {
			logMaintenance(root, "pre-purge force-stop completed in %s; stopped %d process(es): %s", time.Since(started).Round(time.Millisecond), len(processes), processSummary(processes))
		}
		if err := purgeLocalData(context.Background(), root); err != nil {
			fatalf("purge %s: %v", root, err)
		}
	default:
		fatalf("unsupported command %q", opts.Command)
	}
}

func parseCommandOptions(args []string) (commandOptions, error) {
	if len(args) == 0 {
		return commandOptions{}, errors.New("usage: lazymind-installer-maintenance preflight|check-stopped|force-stop|purge-local-data [options]")
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
		case "--temp-dir":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return commandOptions{}, errors.New("--temp-dir requires a path")
			}
			opts.TempDir = filepath.Clean(args[index])
		case "--minimum-free-space-mb":
			index++
			if index >= len(args) {
				return commandOptions{}, errors.New("--minimum-free-space-mb requires a value")
			}
			value, err := strconv.ParseUint(args[index], 10, 64)
			if err != nil || value == 0 {
				return commandOptions{}, errors.New("--minimum-free-space-mb must be a positive integer")
			}
			opts.MinimumFreeSpaceMB = value
		case "--maximum-relative-path-length":
			index++
			if index >= len(args) {
				return commandOptions{}, errors.New("--maximum-relative-path-length requires a value")
			}
			value, err := strconv.Atoi(args[index])
			if err != nil || value <= 0 {
				return commandOptions{}, errors.New("--maximum-relative-path-length must be a positive integer")
			}
			opts.MaximumRelativeLength = value
		default:
			return commandOptions{}, fmt.Errorf("unsupported argument %q", args[index])
		}
	}
	if opts.Command == "preflight" {
		if opts.InstallDir == "" || opts.TempDir == "" || opts.MinimumFreeSpaceMB == 0 || opts.MaximumRelativeLength == 0 {
			return commandOptions{}, errors.New("preflight requires --install-dir, --temp-dir, --minimum-free-space-mb, and --maximum-relative-path-length")
		}
	}
	return opts, nil
}

func installerPreflight(opts commandOptions) error {
	for label, directory := range map[string]string{"installation": opts.InstallDir, "temporary": opts.TempDir} {
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("%s directory is not an absolute path: %q", label, directory)
		}
		if err := requireReliableLocalVolume(directory); err != nil {
			return fmt.Errorf("%s directory %q is unreliable: %w", label, directory, err)
		}
		if err := ensureWritableDirectory(directory); err != nil {
			return fmt.Errorf("%s directory %q is not writable: %w", label, directory, err)
		}
		freeBytes, err := diskFreeBytes(directory)
		if err != nil {
			return fmt.Errorf("cannot inspect free space for %s directory %q: %w", label, directory, err)
		}
		requiredBytes := opts.MinimumFreeSpaceMB * 1024 * 1024
		if freeBytes < requiredBytes {
			return fmt.Errorf("%s directory %q has %.1f GiB free; at least %.1f GiB is required", label, directory, float64(freeBytes)/(1<<30), float64(requiredBytes)/(1<<30))
		}
	}

	projectedLength := windowsPathLength(filepath.Clean(opts.InstallDir)) + 1 + opts.MaximumRelativeLength
	if projectedLength >= 260 {
		return fmt.Errorf("installation path is too long: packaged files may reach %d characters (limit 259); use a Windows account with a shorter Local AppData path", projectedLength)
	}
	return nil
}

func windowsPathLength(path string) int {
	return len(utf16.Encode([]rune(path)))
}

func requireReliableLocalVolume(directory string) error {
	volume := filepath.VolumeName(directory)
	if volume == "" || strings.HasPrefix(volume, `\\`) {
		return errors.New("UNC/network paths are not supported")
	}
	rootPointer, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return err
	}
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getDriveType := kernel32.NewProc("GetDriveTypeW")
	driveType, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(rootPointer)))
	const (
		driveFixed   = 3
		driveRAMDisk = 6
	)
	if driveType != driveFixed && driveType != driveRAMDisk {
		return fmt.Errorf("volume must be a local fixed disk (Windows drive type %d)", driveType)
	}
	return nil
}

func ensureWritableDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(directory, ".lazymind-installer-write-test-")
	if err != nil {
		return err
	}
	name := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(name)
		return closeErr
	}
	return os.Remove(name)
}

func diskFreeBytes(directory string) (uint64, error) {
	pathPointer, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return 0, err
	}
	var freeBytes uint64
	var totalBytes uint64
	var totalFreeBytes uint64
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")
	result, _, callErr := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if result == 0 {
		return 0, callErr
	}
	return freeBytes, nil
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

func purgeLocalData(ctx context.Context, target string) error {
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
	retryOptions := winfile.RetryOptions{MaxWait: purgeRetryLimit}
	if err := winfile.RetryOperation(ctx, func() error {
		return root.Rename(appDataLeaf, tombstone)
	}, retryOptions); err != nil {
		return fmt.Errorf("quarantine data root: %w", err)
	}
	if err := winfile.RetryOperation(ctx, func() error {
		return root.RemoveAll(tombstone)
	}, retryOptions); err != nil {
		if restoreErr := winfile.RetryOperation(ctx, func() error {
			return root.Rename(tombstone, appDataLeaf)
		}, retryOptions); restoreErr != nil {
			return fmt.Errorf("delete quarantined data: %w; restore also failed: %v", err, restoreErr)
		}
		return fmt.Errorf("delete quarantined data: %w", err)
	}
	return nil
}
