package agentexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func platformDSHDesktopRunning(ctx context.Context, cli string) (bool, error) {
	contents := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cli)))))
	if _, err := os.ReadDir(filepath.Join(contents, "MacOS")); err != nil {
		return false, err
	}
	output, err := Run(ctx, "/bin/ps", "-axo", "command=")
	if err != nil {
		return false, err
	}
	return dshProcessSnapshotRunning(contents, output), nil
}

func dshProcessSnapshotRunning(contents, snapshot string) bool {
	for _, line := range strings.Split(snapshot, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "--expose-internals") {
			continue
		}
		if strings.HasPrefix(line, filepath.Join(contents, "MacOS")+"/") ||
			strings.HasPrefix(line, filepath.Join(contents, "Frameworks")+"/") {
			return true
		}
	}
	return false
}
