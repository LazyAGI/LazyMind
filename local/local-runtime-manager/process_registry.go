package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type LocalProcessRecord struct {
	Service     string   `json:"service"`
	PID         int      `json:"pid"`
	PGID        int      `json:"pgid,omitempty"`
	RepoRoot    string   `json:"repoRoot"`
	RuntimeRoot string   `json:"runtimeRoot"`
	Ports       []int    `json:"ports,omitempty"`
	Command     []string `json:"command,omitempty"`
	StartedAt   string   `json:"startedAt"`
}

type localProcessRegistry struct {
	Version   int                  `json:"version"`
	RepoRoot  string               `json:"repoRoot"`
	Processes []LocalProcessRecord `json:"processes"`
}

func registerLocalProcess(paths RuntimePaths, service string, pid int, ports []int, command []string) {
	if pid <= 0 {
		return
	}
	_ = withFileLock(paths.ProcessRegistryFile+".lock", func() error {
		registry, _ := readLocalProcessRegistry(paths)
		registry.Version = 1
		registry.RepoRoot = paths.RepoRoot
		record := LocalProcessRecord{
			Service:     service,
			PID:         pid,
			PGID:        processGroupID(pid),
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
			Ports:       compactPorts(ports),
			Command:     command,
			StartedAt:   time.Now().UTC().Format(time.RFC3339),
		}
		replaced := false
		for i := range registry.Processes {
			if registry.Processes[i].Service == service || registry.Processes[i].PID == pid {
				registry.Processes[i] = record
				replaced = true
				break
			}
		}
		if !replaced {
			registry.Processes = append(registry.Processes, record)
		}
		return writeLocalProcessRegistry(paths, registry)
	})
}

func unregisterLocalProcess(paths RuntimePaths, service string, pid int) {
	_ = withFileLock(paths.ProcessRegistryFile+".lock", func() error {
		registry, err := readLocalProcessRegistry(paths)
		if err != nil {
			return nil
		}
		kept := registry.Processes[:0]
		for _, record := range registry.Processes {
			if service != "" && record.Service == service {
				continue
			}
			if pid > 0 && record.PID == pid {
				continue
			}
			kept = append(kept, record)
		}
		registry.Processes = kept
		if len(registry.Processes) == 0 {
			return os.Remove(paths.ProcessRegistryFile)
		}
		return writeLocalProcessRegistry(paths, registry)
	})
}

func readLocalProcessRegistry(paths RuntimePaths) (localProcessRegistry, error) {
	raw, err := os.ReadFile(paths.ProcessRegistryFile)
	if err != nil {
		return localProcessRegistry{Version: 1, RepoRoot: paths.RepoRoot}, err
	}
	var registry localProcessRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		return localProcessRegistry{Version: 1, RepoRoot: paths.RepoRoot}, err
	}
	return registry, nil
}

func writeLocalProcessRegistry(paths RuntimePaths, registry localProcessRegistry) error {
	if err := os.MkdirAll(filepath.Dir(paths.ProcessRegistryFile), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(paths.ProcessRegistryFile, raw, 0o644)
}

func processGroupID(pid int) int {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return 0
	}
	return pgid
}

func compactPorts(ports []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(ports))
	for _, port := range ports {
		if port <= 0 || port >= 65536 {
			continue
		}
		if _, ok := seen[port]; ok {
			continue
		}
		seen[port] = struct{}{}
		out = append(out, port)
	}
	return out
}

func pidFileRecords(paths RuntimePaths, cfg RuntimeConfig) []LocalProcessRecord {
	files := []struct {
		service string
		path    string
		ports   []int
	}{
		{processComposeServiceName, paths.ProcessComposePIDFile, []int{cfg.ProcessComposePort}},
		{authServiceProcessName, paths.AuthServicePIDFile, []int{cfg.AuthService.Port}},
		{coreProcessName, paths.CorePIDFile, []int{cfg.LocalProxy.CoreHostPort}},
		{scanControlPlaneProcessName, paths.ScanControlPlanePIDFile, []int{cfg.LocalProxy.ScanHostPort}},
		{fileWatcherProcessName, paths.FileWatcherPIDFile, []int{cfg.FileWatcher.Port}},
		{milvusLiteProcessName, paths.MilvusLitePIDFile, []int{cfg.ModeProfile.VectorStore.Port}},
	}
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		files = append(files, struct {
			service string
			path    string
			ports   []int
		}{spec.Name, algorithmPIDFile(paths, spec.Name), []int{spec.Port}})
	}
	records := make([]LocalProcessRecord, 0, len(files))
	for _, file := range files {
		pid := readPIDFileQuiet(file.path)
		if pid <= 0 {
			continue
		}
		records = append(records, LocalProcessRecord{
			Service:     file.service,
			PID:         pid,
			PGID:        processGroupID(pid),
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
			Ports:       compactPorts(file.ports),
		})
	}
	return records
}

func readPIDFileQuiet(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
