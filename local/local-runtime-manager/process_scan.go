package main

import (
	"context"
	"os"
	"sort"
	"time"
)

type localProcessScanner func(RuntimePaths) ([]LocalProcessRecord, error)

func discoverLocalRuntimeProcesses(paths RuntimePaths, cfg RuntimeConfig, scanner localProcessScanner) []LocalProcessRecord {
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
	if scanned, err := scanner(paths); err == nil {
		records = append(records, scanned...)
	}
	return dedupeProcessRecords(records, paths)
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
	if len(records) == 0 {
		return nil
	}
	for _, record := range records {
		_ = interruptProcess(record.PID)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive := false
		for _, record := range records {
			if processAlive(record.PID) {
				alive = true
				break
			}
		}
		if !alive {
			return nil
		}
		select {
		case <-ctx.Done():
			for _, record := range records {
				_ = forceKillProcessTree(record.PID)
			}
			return ctx.Err()
		case <-deadline.C:
			for _, record := range records {
				_ = forceKillProcessTree(record.PID)
			}
			return nil
		case <-ticker.C:
		}
	}
}

func cleanupLocalProcessRecords(paths RuntimePaths, records []LocalProcessRecord) {
	for _, record := range records {
		unregisterLocalProcess(paths, record.Service, record.PID)
	}
}
