package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

var composeBindCriticalReadPaths = []string{
	"backend/scan-control-plane/migrations",
	"backend/scan-control-plane/scripts",
	"backend/file-watcher/configs",
	"db-init",
	"kong/plugins",
	"plugins",
	"scripts/db-bootstrap.sh",
	"kong.yml",
	"redis-users.acl",
}

var composeBindBestEffortReadPaths = []string{
	"api/backend",
	"evo",
}

func ensureComposeBindPermissions(repoRoot string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := addContainerReadBits(repoRoot); err != nil {
		return fmt.Errorf("ensure repo root is readable by containers: %w", err)
	}
	for _, rel := range composeBindCriticalReadPaths {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect compose bind path %s: %w", rel, err)
		}
		if err := makeTreeContainerReadable(path); err != nil {
			return fmt.Errorf("ensure compose bind path %s is readable by containers: %w", rel, err)
		}
	}
	for _, rel := range composeBindBestEffortReadPaths {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		_ = makeTreeContainerReadable(path)
	}
	return nil
}

func makeTreeContainerReadable(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return addContainerReadBits(path)
	})
}

func addContainerReadBits(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm() | 0o444
	if info.IsDir() || mode&0o111 != 0 {
		mode |= 0o111
	}
	return os.Chmod(path, mode)
}
