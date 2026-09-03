//go:build darwin

package agentexec

import (
	"os"
	"path/filepath"
	"strings"
)

func platformDesktopInstalled(spec DesktopApplication, _ bool) bool {
	return platformDesktopApplication(spec) != ""
}

func platformDesktopApplication(spec DesktopApplication) string {
	wanted := make(map[string]struct{}, len(spec.DisplayNames))
	for _, name := range spec.DisplayNames {
		name = strings.TrimSpace(strings.TrimSuffix(name, ".app"))
		if name != "" {
			wanted[strings.ToLower(name)] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return ""
	}
	for _, root := range darwinApplicationDirectories() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.EqualFold(filepath.Ext(name), ".app") {
				continue
			}
			if _, ok := wanted[strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))]; !ok {
				continue
			}
			if info, err := os.Stat(filepath.Join(root, name)); err == nil && info.IsDir() {
				return filepath.Join(root, name)
			}
		}
	}
	return ""
}

func darwinApplicationDirectories() []string {
	if configured := strings.TrimSpace(os.Getenv("LAZYMIND_DESKTOP_APPLICATION_DIRS")); configured != "" {
		return filepath.SplitList(configured)
	}
	directories := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		directories = append(directories, filepath.Join(home, "Applications"))
	}
	return directories
}
