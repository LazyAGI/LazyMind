//go:build darwin

package agentexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	darwinShellPathTimeout = 3 * time.Second
	darwinShellPathTTL     = 5 * time.Second
	darwinShellOutputLimit = 64 << 10
)

var darwinBundleExecutableDirectories = []string{"MacOS", "Resources"}

var darwinShellPathCache struct {
	sync.Mutex
	key       string
	value     string
	expiresAt time.Time
}

func platformExecutableCandidates(names []string) []string {
	searchPath := effectiveDarwinPath()
	candidates := make([]string, 0, len(names))
	for _, name := range names {
		if path := lookPathInDarwin(name, searchPath); path != "" {
			candidates = append(candidates, path)
		}
	}
	return uniqueDarwinPaths(append(candidates, darwinBundleExecutables(names)...))
}

func resolvePlatformExecutable(value string) (string, bool, error) {
	if filepath.IsAbs(value) || strings.ContainsRune(value, filepath.Separator) {
		return "", false, nil
	}
	resolved := lookPathInDarwin(value, effectiveDarwinPath())
	if resolved == "" {
		return "", true, exec.ErrNotFound
	}
	return resolved, true, nil
}

func platformSafeEnvironment(environment []string) []string {
	searchPath := effectiveDarwinPath()
	if searchPath == "" {
		return environment
	}
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name != "PATH" {
			result = append(result, entry)
		}
	}
	return append(result, "PATH="+searchPath)
}

func effectiveDarwinPath() string {
	return mergeDarwinPath(os.Getenv("PATH"), darwinLoginShellPath())
}

func darwinLoginShellPath() string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return ""
	}
	key := shell + "\x00" + os.Getenv("PATH")
	now := time.Now()
	darwinShellPathCache.Lock()
	if darwinShellPathCache.key == key && now.Before(darwinShellPathCache.expiresAt) {
		value := darwinShellPathCache.value
		darwinShellPathCache.Unlock()
		return value
	}
	darwinShellPathCache.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), darwinShellPathTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "-lic", "exec /usr/bin/printenv PATH")
	command.Env = os.Environ()
	stdout := cappedBuffer{Limit: darwinShellOutputLimit}
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return cacheDarwinShellPath(key, "", now)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	value := strings.TrimSpace(lines[len(lines)-1])
	return cacheDarwinShellPath(key, value, now)
}

func cacheDarwinShellPath(key, value string, now time.Time) string {
	darwinShellPathCache.Lock()
	darwinShellPathCache.key = key
	darwinShellPathCache.value = value
	darwinShellPathCache.expiresAt = now.Add(darwinShellPathTTL)
	darwinShellPathCache.Unlock()
	return value
}

func mergeDarwinPath(values ...string) string {
	seen := map[string]bool{}
	paths := make([]string, 0)
	for _, value := range values {
		for _, directory := range filepath.SplitList(value) {
			directory = strings.TrimSpace(directory)
			if directory == "" {
				continue
			}
			cleaned := filepath.Clean(directory)
			if !seen[cleaned] {
				seen[cleaned] = true
				paths = append(paths, cleaned)
			}
		}
	}
	return strings.Join(paths, string(os.PathListSeparator))
}

func lookPathInDarwin(name, searchPath string) string {
	if strings.TrimSpace(name) == "" || strings.ContainsRune(name, filepath.Separator) {
		return ""
	}
	for _, directory := range filepath.SplitList(searchPath) {
		if resolved := executableFile(filepath.Join(directory, name)); resolved != "" {
			return resolved
		}
	}
	return ""
}

func darwinBundleExecutables(names []string) []string {
	var candidates []string
	for _, bundle := range desktopApplicationBindings() {
		candidates = append(candidates, darwinBundleExecutablePaths(bundle, names)...)
	}
	for _, root := range darwinApplicationDirectories() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".app") {
				continue
			}
			candidates = append(candidates, darwinBundleExecutablePaths(
				filepath.Join(root, entry.Name()), names,
			)...)
		}
	}
	return candidates
}

func darwinBundleExecutablePaths(bundle string, names []string) []string {
	if !strings.EqualFold(filepath.Ext(bundle), ".app") {
		return nil
	}
	contents := filepath.Join(bundle, "Contents")
	var candidates []string
	for _, name := range names {
		for _, directory := range darwinBundleExecutableDirectories {
			if resolved := executableFile(filepath.Join(contents, directory, name)); resolved != "" {
				candidates = append(candidates, resolved)
			}
		}
	}
	return candidates
}

func executableFile(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	resolved, err := ResolveExecutable(path)
	if err != nil {
		return ""
	}
	return resolved
}

func uniqueDarwinPaths(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.Clean(strings.TrimSpace(value))
		if value != "." && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
