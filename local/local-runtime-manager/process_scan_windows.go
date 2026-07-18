//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const windowsProcessScanTimeout = 10 * time.Second
const desktopOwnerPIDEnvVar = "LAZYMIND_DESKTOP_OWNER_PID"

type windowsProcessInfo struct {
	ProcessID       uint32  `json:"ProcessId"`
	ParentProcessID uint32  `json:"ParentProcessId"`
	ExecutablePath  *string `json:"ExecutablePath"`
	CommandLine     *string `json:"CommandLine"`
}

// Windows has no /proc command-line surface. CIM is the native, supported
// Windows inventory API and lets the orphan scanner match Python/Node children
// whose executable itself lives outside the LazyMind runtime tree.
func scanLocalRuntimeProcesses(paths RuntimePaths) ([]LocalProcessRecord, error) {
	command := "$p=@(Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,ExecutablePath,CommandLine); ConvertTo-Json -InputObject $p -Compress"
	ctx, cancel := context.WithTimeout(context.Background(), windowsProcessScanTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	configureChildProcess(cmd, false)
	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Windows process scan timed out after %s: %w", windowsProcessScanTimeout, ctx.Err())
		}
		return nil, err
	}
	var processes []windowsProcessInfo
	if err := json.Unmarshal(raw, &processes); err != nil {
		return nil, err
	}
	parents := make(map[int]int, len(processes))
	for _, process := range processes {
		parents[int(process.ProcessID)] = int(process.ParentProcessID)
	}
	excluded := map[int]bool{os.Getpid(): true}
	for pid := parents[os.Getpid()]; pid > 0 && !excluded[pid]; pid = parents[pid] {
		excluded[pid] = true
	}
	ownerPID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv(desktopOwnerPIDEnvVar)))
	if ownerPID > 0 {
		excludeProcessTree(excluded, parents, ownerPID)
	}
	records := make([]LocalProcessRecord, 0, len(processes))
	for _, process := range processes {
		pid := int(process.ProcessID)
		if pid <= 0 || excluded[pid] {
			continue
		}
		exe := nullableText(process.ExecutablePath)
		cmdline := nullableText(process.CommandLine)
		if !processTextMatchesRuntime(paths, exe, cmdline) {
			continue
		}
		records = append(records, LocalProcessRecord{
			Service:     inferServiceFromProcessText(paths, exe+" "+cmdline),
			PID:         pid,
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
			Command:     []string{strings.TrimSpace(exe), strings.TrimSpace(cmdline), strconv.Itoa(pid)},
			StartID:     processStartIdentity(pid),
		})
	}
	return records, nil
}

func excludeProcessTree(excluded map[int]bool, parents map[int]int, rootPID int) {
	if rootPID <= 0 {
		return
	}
	excluded[rootPID] = true
	for pid := range parents {
		seen := map[int]bool{}
		for ancestor := pid; ancestor > 0 && !seen[ancestor]; ancestor = parents[ancestor] {
			if ancestor == rootPID {
				excluded[pid] = true
				break
			}
			seen[ancestor] = true
		}
	}
}

func nullableText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
