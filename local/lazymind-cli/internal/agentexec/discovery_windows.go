//go:build windows

package agentexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	windowsExecutableScanDepth = 2
	windowsExecutableScanLimit = 2048
)

func platformExecutableCandidates(names []string) []string {
	candidates := make([]string, 0, len(names)*2)
	pathValue := effectiveWindowsPath()
	pathExt := windowsPathExtensions()
	for _, name := range names {
		if path := lookPathIn(name, pathValue, pathExt); path != "" {
			candidates = append(candidates, path)
		}
		if path := appPath(name); path != "" {
			candidates = append(candidates, path)
		}
	}
	candidates = append(candidates, installedWindowsExecutables(names, pathExt)...)
	return uniqueWindowsPaths(candidates)
}

func installedWindowsExecutables(names, extensions []string) []string {
	var roots []string
	for _, application := range desktopApplicationBindings() {
		info, err := os.Stat(application)
		if err == nil {
			if info.IsDir() {
				roots = append(roots, application)
			} else {
				roots = append(roots, filepath.Dir(application))
			}
		}
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		roots = append(roots, filepath.Join(localAppData, "Programs"))
	}
	for _, application := range windowsInstalledApplications() {
		if directoryExists(application.location) {
			roots = append(roots, application.location)
		}
	}
	roots = uniqueWindowsPaths(roots)
	var candidates []string
	remaining := windowsExecutableScanLimit
	for _, root := range roots {
		candidates = append(candidates, findWindowsExecutables(
			root, names, extensions, windowsExecutableScanDepth, &remaining,
		)...)
		if remaining == 0 {
			break
		}
	}
	return uniqueWindowsPaths(candidates)
}

func findWindowsExecutables(
	root string,
	names, extensions []string,
	depth int,
	remaining *int,
) []string {
	if depth < 0 || *remaining == 0 || !directoryExists(root) {
		return nil
	}
	(*remaining)--
	var candidates []string
	for _, name := range names {
		if candidate := firstExecutablePath(filepath.Join(root, name), extensions); candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	if depth == 0 {
		return candidates
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return candidates
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidates = append(candidates, findWindowsExecutables(
			filepath.Join(root, entry.Name()), names, extensions, depth-1, remaining,
		)...)
		if *remaining == 0 {
			break
		}
	}
	return candidates
}

func resolvePlatformExecutable(value string) (string, bool, error) {
	resolved := lookPathIn(value, effectiveWindowsPath(), windowsPathExtensions())
	if resolved == "" {
		return "", true, exec.ErrNotFound
	}
	return resolved, true, nil
}

func effectiveWindowsPath() string {
	machinePath := registryString(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`,
		"Path",
		registry.WOW64_64KEY,
	)
	userPath := registryString(registry.CURRENT_USER, `Environment`, "Path", registry.WOW64_64KEY)
	return mergeWindowsPath(os.Getenv("PATH"), machinePath, userPath, os.Getenv("LOCALAPPDATA"))
}

func platformSafeEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "PATH") {
			result = append(result, entry)
		}
	}
	return append(result, "PATH="+effectiveWindowsPath())
}

func mergeWindowsPath(processPath, machinePath, userPath, localAppData string) string {
	values := filepath.SplitList(processPath)
	for _, value := range []string{machinePath, userPath} {
		values = append(values, filepath.SplitList(value)...)
	}
	if localAppData = strings.TrimSpace(localAppData); localAppData != "" {
		values = append(values, filepath.Join(localAppData, "Microsoft", "WindowsApps"))
	}
	return strings.Join(uniqueWindowsPaths(values), string(os.PathListSeparator))
}

func windowsPathExtensions() []string {
	value := strings.TrimSpace(os.Getenv("PATHEXT"))
	if value == "" {
		value = ".COM;.EXE;.BAT;.CMD"
	}
	extensions := strings.Split(value, ";")
	for index, extension := range extensions {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension != "" && !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		extensions[index] = extension
	}
	return extensions
}

func lookPathIn(name, pathValue string, extensions []string) string {
	if strings.ContainsAny(name, `:\/`) {
		return firstExecutablePath(name, extensions)
	}
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			continue
		}
		if candidate := firstExecutablePath(filepath.Join(directory, name), extensions); candidate != "" {
			return candidate
		}
	}
	return ""
}

func firstExecutablePath(path string, extensions []string) string {
	if filepath.Ext(path) != "" {
		if fileExists(path) {
			return filepath.Clean(path)
		}
	}
	for _, extension := range extensions {
		if extension == "" {
			continue
		}
		candidate := path + extension
		if fileExists(candidate) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

func appPath(name string) string {
	base := filepath.Base(name)
	if filepath.Ext(base) == "" {
		base += ".exe"
	}
	keyPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\` + base
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			if value := commandExecutable(registryString(root, keyPath, "", view)); fileExists(value) {
				return filepath.Clean(value)
			}
		}
	}
	return ""
}

func registryString(root registry.Key, path, name string, view uint32) string {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|view)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, valueType, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	value = strings.TrimSpace(value)
	if valueType == registry.EXPAND_SZ {
		if expanded, expandErr := registry.ExpandString(value); expandErr == nil {
			value = expanded
		}
	}
	return value
}

func uniqueWindowsPaths(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(value))
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
