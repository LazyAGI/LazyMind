//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
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
			if err := os.WriteFile(destination, rawExecutable, 0o755); err != nil {
				return fmt.Errorf("replace relocatable desktop Python executable %s: %w", destination, err)
			}
		}
	}
	return nil
}
