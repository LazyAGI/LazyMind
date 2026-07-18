//go:build windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	desktopPythonReplaceTimeout = 5 * time.Second
	desktopPythonReplacePoll    = 250 * time.Millisecond
)

func relocateDesktopPythonVenvs(cfg RuntimeConfig, paths RuntimePaths) error {
	if cfg.Profile != "desktop" {
		return nil
	}
	for _, venv := range []string{paths.AuthServiceVenvDir, paths.AlgorithmVenv} {
		configPath := filepath.Join(venv, "pyvenv.cfg")
		raw, err := os.ReadFile(configPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read desktop Python venv config %s: %w", configPath, err)
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		rewritten := false
		var newHome string
		for i, line := range lines {
			if !strings.HasPrefix(strings.TrimSpace(line), "home =") {
				continue
			}
			oldHome := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "home ="))
			newHome = filepath.Join(paths.PythonRuntimeDir, filepath.Base(oldHome))
			if info, statErr := os.Stat(filepath.Join(newHome, "python.exe")); statErr != nil || info.IsDir() {
				return fmt.Errorf("bundled desktop Python runtime not found: %s", newHome)
			}
			lines[i] = "home = " + newHome
			rewritten = true
			break
		}
		if !rewritten {
			return fmt.Errorf("desktop Python venv config has no home entry: %s", configPath)
		}
		if err := replaceRelocatableFileIfChanged(configPath, []byte(strings.Join(lines, "\n")), 0o644, desktopPythonReplaceTimeout); err != nil {
			return fmt.Errorf("rewrite desktop Python venv config %s: %w", configPath, err)
		}
		entries, err := os.ReadDir(newHome)
		if err != nil {
			return fmt.Errorf("read bundled desktop Python runtime %s: %w", newHome, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || (name != "python.exe" && name != "pythonw.exe" && !strings.HasSuffix(strings.ToLower(name), ".dll")) {
				continue
			}
			source := filepath.Join(newHome, name)
			rawExecutable, readErr := os.ReadFile(source)
			if readErr != nil {
				return fmt.Errorf("read bundled desktop Python executable %s: %w", source, readErr)
			}
			destination := filepath.Join(venv, "Scripts", name)
			if err := replaceRelocatableFileIfChanged(destination, rawExecutable, 0o755, desktopPythonReplaceTimeout); err != nil {
				return fmt.Errorf("replace relocatable desktop Python executable %s: %w", destination, err)
			}
		}
	}
	return nil
}

func replaceRelocatableFileIfChanged(destination string, content []byte, mode os.FileMode, timeout time.Duration) error {
	temp, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for {
		current, readErr := os.ReadFile(destination)
		if readErr == nil && bytes.Equal(current, content) {
			return nil
		}
		if readErr != nil && !os.IsNotExist(readErr) && !isWindowsFileBusy(readErr) {
			return readErr
		}
		if readErr == nil || os.IsNotExist(readErr) {
			source, sourceErr := windows.UTF16PtrFromString(tempPath)
			if sourceErr != nil {
				return sourceErr
			}
			target, targetErr := windows.UTF16PtrFromString(destination)
			if targetErr != nil {
				return targetErr
			}
			err = windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
			if err == nil {
				return nil
			}
			if !isWindowsFileBusy(err) {
				return err
			}
		} else {
			err = readErr
		}
		if timeout <= 0 || time.Now().Add(desktopPythonReplacePoll).After(deadline) {
			return fmt.Errorf("file is still locked after %s: %w", timeout, err)
		}
		time.Sleep(desktopPythonReplacePoll)
	}
}

func isWindowsFileBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
