//go:build windows

package main

import (
	"context"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winprocess"
)

const windowsProcessScanTimeout = 30 * time.Second

// Observe process paths and creation identities through native handles. Runtime
// registrations seed ownership; the common selector follows their descendants.
func scanLocalRuntimeProcesses(paths RuntimePaths) ([]LocalProcessRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), windowsProcessScanTimeout)
	defer cancel()
	processes, err := winprocess.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]LocalProcessRecord, 0, len(processes))
	for _, process := range processes {
		pid := int(process.ProcessID)
		if pid <= 0 {
			continue
		}
		exe := winprocess.Text(process.ExecutablePath)
		records = append(records, LocalProcessRecord{
			PID: pid, ParentPID: int(process.ParentProcessID), StartID: process.StartID,
			Executable: exe, Command: []string{exe},
		})
	}
	return records, nil
}
