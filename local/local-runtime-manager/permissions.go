package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

func ensureLocalDataRootWritable(repoRoot string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dataRoot := filepath.Join(repoRoot, "data")
	if err := ensureWritableDir(dataRoot); err == nil {
		return nil
	} else if os.IsPermission(err) {
		if repairErr := repairLocalDataRootWithDocker(repoRoot); repairErr != nil {
			return fmt.Errorf("local data root %s is not writable and docker repair failed: %w", dataRoot, repairErr)
		}
		if retryErr := ensureWritableDir(dataRoot); retryErr != nil {
			return fmt.Errorf("local data root %s is still not writable after docker repair: %w", dataRoot, retryErr)
		}
		return nil
	} else {
		return err
	}
}

func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe := filepath.Join(dir, ".lazymind-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(probe)
}

func repairLocalDataRootWithDocker(repoRoot string) error {
	image := envText("POSTGRES_IMAGE", "postgres:16")
	uid := strconv.Itoa(os.Getuid())
	gid := strconv.Itoa(os.Getgid())
	cmd := exec.Command(
		"docker",
		"run",
		"--rm",
		"-v", repoRoot+":/work",
		"-w", "/work",
		image,
		"sh",
		"-lc",
		"mkdir -p data && chown -R "+uid+":"+gid+" data",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w (%s)", err, string(out))
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
