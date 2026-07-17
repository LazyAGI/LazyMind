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
		return dedupeProcessRecords(records, paths), fmt.Errorf("scan local runtime processes: %w", err)
	}
	records = append(records, scanned...)
	return dedupeProcessRecords(records, paths), nil
}

func localProcessBelongsToRuntime(record LocalProcessRecord, paths RuntimePaths) bool {
	if record.PID <= 0 {
		return false
	}
	return record.RepoRoot == paths.RepoRoot || record.RuntimeRoot == paths.RuntimeRoot
}

func dedupeProcessRecords(records []LocalProcessRecord, paths RuntimePaths) []LocalProcessRecord {
	byPID := map[int]LocalProcessRecord{}
	for _, record := range records {
		if record.PID <= 0 || !processAlive(record.PID) || record.PID == os.Getpid() {
			continue
		}
		if record.StartID != 0 {
			if current := processStartIdentity(record.PID); current != 0 && current != record.StartID {
				continue
			}
		}
		if !localProcessBelongsToRuntime(record, paths) {
			continue
		}
		if record.PGID == 0 {
			record.PGID = processGroupID(record.PID)
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
		interrupt:       interruptProcess,
		forceKill:       forceKillProcessTree,
		alive:           processAlive,
		gracefulTimeout: 10 * time.Second,
		forceTimeout:    5 * time.Second,
		pollInterval:    250 * time.Millisecond,
	})
}

type processStopOptions struct {
	interrupt       func(int) error
	forceKill       func(int) error
	alive           func(int) bool
	gracefulTimeout time.Duration
	forceTimeout    time.Duration
	pollInterval    time.Duration
}

func stopLocalProcessRecordsWith(ctx context.Context, records []LocalProcessRecord, options processStopOptions) error {
	if len(records) == 0 {
		return nil
	}
	for _, record := range records {
		_ = options.interrupt(record.PID)
	}
	deadline := time.NewTimer(options.gracefulTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(options.pollInterval)
	defer ticker.Stop()
	for {
		remaining := aliveLocalProcessRecords(records, options.alive)
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			for _, record := range remaining {
				_ = options.forceKill(record.PID)
			}
			return ctx.Err()
		case <-deadline.C:
			for _, record := range remaining {
				_ = options.forceKill(record.PID)
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
		remaining := aliveLocalProcessRecords(records, options.alive)
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			remaining = aliveLocalProcessRecords(records, options.alive)
			return fmt.Errorf("local runtime processes did not exit after force kill: %s", summarizeLocalProcessRecords(remaining))
		case <-ticker.C:
		}
	}
}

func aliveLocalProcessRecords(records []LocalProcessRecord, alive func(int) bool) []LocalProcessRecord {
	remaining := make([]LocalProcessRecord, 0, len(records))
	for _, record := range records {
		if alive(record.PID) {
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
	for _, record := range records {
		unregisterLocalProcess(paths, record.Service, record.PID)
	}
}
