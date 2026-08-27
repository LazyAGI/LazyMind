//go:build windows

package agentexec

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func platformDesktopInstalled(spec DesktopApplication, _ bool) bool {
	for _, name := range spec.ExecutableNames {
		if appPath(name) != "" {
			return true
		}
	}
	for _, protocol := range spec.Protocols {
		if registeredProtocolExecutable(protocol) != "" {
			return true
		}
	}
	return hasInstalledApplication(spec.DisplayNames)
}

func registeredProtocolExecutable(protocol string) string {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		return ""
	}
	path := `Software\Classes\` + protocol + `\shell\open\command`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			if executable := commandExecutable(registryString(root, path, "", view)); fileExists(executable) {
				return filepath.Clean(executable)
			}
		}
	}
	return ""
}

func hasInstalledApplication(displayNames []string) bool {
	if len(displayNames) == 0 {
		return false
	}
	wanted := make(map[string]bool, len(displayNames))
	for _, name := range displayNames {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			wanted[name] = true
		}
	}
	const uninstallPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			key, err := registry.OpenKey(root, uninstallPath, registry.ENUMERATE_SUB_KEYS|view)
			if err != nil {
				continue
			}
			names, err := key.ReadSubKeyNames(-1)
			_ = key.Close()
			if err != nil {
				continue
			}
			for _, name := range names {
				entry := uninstallPath + `\` + name
				displayName := strings.ToLower(registryString(root, entry, "DisplayName", view))
				if !wanted[displayName] {
					continue
				}
				if directoryExists(registryString(root, entry, "InstallLocation", view)) ||
					fileExists(commandExecutable(registryString(root, entry, "DisplayIcon", view))) ||
					fileExists(commandExecutable(registryString(root, entry, "UninstallString", view))) {
					return true
				}
			}
		}
	}
	return false
}

func commandExecutable(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
	}
	if end := strings.IndexAny(command, " \t,"); end >= 0 {
		return command[:end]
	}
	return command
}

func directoryExists(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	return err == nil && info.IsDir()
}
