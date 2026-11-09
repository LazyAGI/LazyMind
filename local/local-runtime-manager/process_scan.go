package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type localProcessScanner func(RuntimePaths) ([]LocalProcessRecord, error)

func discoverLocalRuntimeProcesses(paths RuntimePaths, cfg RuntimeConfig, scanner localProcessScanner) []LocalProcessRecord {
	records, _ := discoverLocalRuntimeProcessesChecked(paths, cfg, scanner)
	return records
}

func discoverLocalRuntimeProcessesChecked(paths RuntimePaths, cfg RuntimeConfig, scanner localProcessScanner) ([]LocalProcessRecord, error) {
	return discoverLocalRuntimeProcessesWithDiagnostics(paths, cfg, scanner, nil)
}

func discoverLocalRuntimeProcessesWithDiagnostics(paths RuntimePaths, cfg RuntimeConfig, scanner localProcessScanner, skipped func(LocalProcessRecord)) ([]LocalProcessRecord, error) {
	records := make([]LocalProcessRecord, 0, 32)
	records = append(records, pidFileRecords(paths, cfg)...)
	if registry, err := readLocalProcessRegistry(paths); err == nil {
		for _, record := range registry.Processes {
			if localProcessBelongsToRuntime(record, paths) {
				records = append(records, record)
			}
		}
	}
	if scanner == nil {
		scanner = scanLocalRuntimeProcesses
	}
	scanned, err := scanner(paths)
	if err != nil {
		// Status can still display registered processes. Cleanup callers must
		// handle the error before attempting to signal any returned record.
		return dedupeProcessRecords(records, paths), fmt.Errorf("scan local runtime processes: %w", err)
	}
	return dedupeProcessRecords(ownedRuntimeProcesses(paths, records, scanned, skipped), paths), nil
}

func localProcessBelongsToRuntime(record LocalProcessRecord, paths RuntimePaths) bool {
	if record.PID <= 0 {
		return false
	}
	if record.RuntimeRoot != "" {
		return sameProcessPath(record.RuntimeRoot, paths.RuntimeRoot)
	}
	return sameProcessPath(record.RepoRoot, paths.RepoRoot)
}

func dedupeProcessRecords(records []LocalProcessRecord, paths RuntimePaths) []LocalProcessRecord {
	byPID := map[int]LocalProcessRecord{}
	currentProcessGroup := processGroupID(os.Getpid())
	for _, record := range records {
		if record.PID <= 0 || !processAlive(record.PID) || record.PID == os.Getpid() {
			continue
		}
		if record.StartID != 0 {
			if current := processStartIdentity(record.PID); current != record.StartID {
				continue
			}
		}
		if !localProcessBelongsToRuntime(record, paths) {
			continue
		}
		if record.PGID == 0 {
			record.PGID = processGroupID(record.PID)
		}
		// Keep the launcher's group excluded as an additional guard for active
		// local-up/local-down commands, even though cleanup now signals by PID.
		if currentProcessGroup > 0 && record.PGID == currentProcessGroup {
			continue
		}
		existing, ok := byPID[record.PID]
		if !ok || existing.Service == "" {
			byPID[record.PID] = record
		}
	}
	out := make([]LocalProcessRecord, 0, len(byPID))
	for _, record := range byPID {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

func stopLocalProcessRecords(ctx context.Context, records []LocalProcessRecord) error {
	return stopLocalProcessRecordsWith(ctx, records, processStopOptions{
		interrupt:       interruptProcessPID,
		forceKill:       forceKillProcessPID,
		alive:           processAlive,
		identity:        processStartIdentity,
		gracefulTimeout: 10 * time.Second,
		forceTimeout:    5 * time.Second,
		pollInterval:    250 * time.Millisecond,
	})
}

type processStopOptions struct {
	interrupt       func(int) error
	forceKill       func(int) error
	alive           func(int) bool
	identity        func(int) uint64
	gracefulTimeout time.Duration
	forceTimeout    time.Duration
	pollInterval    time.Duration
}

func stopLocalProcessRecordsWith(ctx context.Context, records []LocalProcessRecord, options processStopOptions) error {
	if len(records) == 0 {
		return nil
	}
	for _, record := range records {
		if !sameStopProcess(record, options) {
			continue
		}
		// A scanned orphan has no managed service lifecycle to preserve. In
		// particular, Python's multiprocessing resource tracker ignores SIGINT
		// and SIGTERM, so the normal graceful group interrupt cannot stop it.
		// Kill the individual process instead of trusting a stale process group.
		if record.Service == "local-runtime-orphan" {
			_ = options.forceKill(record.PID)
			continue
		}
		_ = options.interrupt(record.PID)
	}
	deadline := time.NewTimer(options.gracefulTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(options.pollInterval)
	defer ticker.Stop()
	for {
		remaining := aliveLocalProcessRecords(records, options)
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			for _, record := range remaining {
				if sameStopProcess(record, options) {
					_ = options.forceKill(record.PID)
				}
			}
			return ctx.Err()
		case <-deadline.C:
			for _, record := range remaining {
				if sameStopProcess(record, options) {
					_ = options.forceKill(record.PID)
				}
			}
			return waitForStoppedLocalProcessRecords(ctx, records, options)
		case <-ticker.C:
		}
	}
}

func waitForStoppedLocalProcessRecords(ctx context.Context, records []LocalProcessRecord, options processStopOptions) error {
	deadline := time.NewTimer(options.forceTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(options.pollInterval)
	defer ticker.Stop()
	for {
		remaining := aliveLocalProcessRecords(records, options)
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			remaining = aliveLocalProcessRecords(records, options)
			return fmt.Errorf("local runtime processes did not exit after force kill: %s", summarizeLocalProcessRecords(remaining))
		case <-ticker.C:
		}
	}
}

func sameStopProcess(record LocalProcessRecord, options processStopOptions) bool {
	return options.identity == nil || (record.StartID != 0 && options.identity(record.PID) == record.StartID)
}

func aliveLocalProcessRecords(records []LocalProcessRecord, options processStopOptions) []LocalProcessRecord {
	remaining := make([]LocalProcessRecord, 0, len(records))
	for _, record := range records {
		if sameStopProcess(record, options) && options.alive(record.PID) {
			remaining = append(remaining, record)
		}
	}
	return remaining
}

func summarizeLocalProcessRecords(records []LocalProcessRecord) string {
	if len(records) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(records))
	for _, record := range records {
		service := strings.TrimSpace(record.Service)
		if service == "" {
			service = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s(pid=%d)", service, record.PID))
	}
	return strings.Join(parts, ", ")
}

func cleanupLocalProcessRecords(paths RuntimePaths, records []LocalProcessRecord) {
	_ = withFileLock(paths.ProcessRegistryFile+".lock", func() error {
		registry, err := readLocalProcessRegistry(paths)
		if err != nil {
			return nil
		}
		stopped := make(map[int]uint64, len(records))
		for _, record := range records {
			stopped[record.PID] = record.StartID
		}
		kept := registry.Processes[:0]
		for _, record := range registry.Processes {
			if identity, ok := stopped[record.PID]; ok && identity != 0 && identity == record.StartID {
				continue
			}
			kept = append(kept, record)
		}
		registry.Processes = kept
		if len(kept) == 0 {
			return os.Remove(paths.ProcessRegistryFile)
		}
		return writeLocalProcessRegistry(paths, registry)
	})
}
