package workbuddy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
)

func bundledRuntime(application, configDir string) (runtimeCommand, error) {
	var script string
	switch runtime.GOOS {
	case "darwin":
		script = filepath.Join(
			application, "Contents", "Resources",
			"app.asar.unpacked", "cli", "bin", "codebuddy",
		)
	case "windows":
		script = filepath.Join(
			filepath.Dir(application), "resources",
			"app.asar.unpacked", "cli", "bin", "codebuddy",
		)
	default:
		return runtimeCommand{}, errors.New("WorkBuddy Desktop is not supported on this platform")
	}
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		return runtimeCommand{}, errors.New("bundled execution engine is missing")
	}
	node, err := managedNodeRuntime(configDir)
	if err != nil {
		return runtimeCommand{}, err
	}
	return runtimeCommand{Binary: node, Arguments: []string{script}}, nil
}

func managedNodeRuntime(configDir string) (string, error) {
	versionsDir := filepath.Join(configDir, "binaries", "node", "versions")
	if current, err := os.ReadFile(filepath.Join(versionsDir, "current")); err == nil {
		if node := nodeRuntimePath(versionsDir, strings.TrimSpace(string(current))); node != "" {
			return node, nil
		}
	}
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return "", errors.New("WorkBuddy has not finished installing its execution runtime; open WorkBuddy once")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.Contains(entry.Name(), ".installing.") &&
			!strings.Contains(entry.Name(), ".__extract_temp__") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		if node := nodeRuntimePath(versionsDir, name); node != "" {
			return node, nil
		}
	}
	return "", errors.New("WorkBuddy has not finished installing its execution runtime; open WorkBuddy once")
}

func nodeRuntimePath(versionsDir, version string) string {
	if version == "" || filepath.Base(version) != version ||
		strings.Contains(version, ".installing.") || strings.Contains(version, ".__extract_temp__") {
		return ""
	}
	path := filepath.Join(versionsDir, version, "bin", "node")
	if runtime.GOOS == "windows" {
		path = filepath.Join(versionsDir, version, "node.exe")
	}
	resolved, err := agentexec.ResolveExecutable(path)
	if err != nil {
		return ""
	}
	return resolved
}
