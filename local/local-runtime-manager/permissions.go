package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func ensureLocalDataRootWritable(repoRoot string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dataRoot := filepath.Join(repoRoot, "data")
	if err := ensureWritableDir(dataRoot); err == nil {
		return nil
	} else if os.IsPermission(err) {
		return fmt.Errorf("local data root %s is not writable; fix ownership or permissions and retry", dataRoot)
	} else {
		return err
	}
}

func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".lazymind-write-test-*")
	if err != nil {
		return err
	}
	probe := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(probe)
		return err
	}
	return os.Remove(probe)
}
