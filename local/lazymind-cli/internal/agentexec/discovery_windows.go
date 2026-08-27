//go:build windows

package agentexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
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
	return uniqueWindowsPaths(candidates)
}

func resolvePlatformExecutable(value string) (string, bool, error) {
	resolved, err := exec.LookPath(value)
	return resolved, true, err
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
			if value := registryString(root, keyPath, "", view); fileExists(value) {
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
	return strings.Trim(value, `"`)
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
