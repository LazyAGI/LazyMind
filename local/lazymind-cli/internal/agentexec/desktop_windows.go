//go:build windows

package agentexec

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type windowsInstalledApplication struct {
	displayName string
	location    string
}

func platformDesktopInstalled(spec DesktopApplication, _ bool) bool {
	return platformDesktopApplication(spec) != "" || hasInstalledApplication(spec.DisplayNames)
}

func platformDesktopApplication(spec DesktopApplication) string {
	for _, name := range spec.ExecutableNames {
		if executable := appPath(name); executable != "" {
			return executable
		}
	}
	for _, protocol := range spec.Protocols {
		if executable := registeredProtocolExecutable(protocol); executable != "" {
			return executable
		}
	}
	for _, application := range windowsInstalledApplications() {
		if !matchesInstalledDisplayName(application.displayName, normalizedDisplayNames(spec.DisplayNames)) {
			continue
		}
		if fileExists(application.location) {
			return filepath.Clean(application.location)
		}
		if directoryExists(application.location) {
			for _, name := range spec.ExecutableNames {
				if executable := firstExecutablePath(filepath.Join(application.location, name), windowsPathExtensions()); executable != "" {
					return executable
				}
			}
		}
	}
	return ""
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
	wanted := normalizedDisplayNames(displayNames)
	for _, application := range windowsInstalledApplications() {
		if !matchesInstalledDisplayName(application.displayName, wanted) {
			continue
		}
		if directoryExists(application.location) || fileExists(application.location) {
			return true
		}
	}
	return false
}

func normalizedDisplayNames(displayNames []string) []string {
	wanted := make([]string, 0, len(displayNames))
	for _, name := range displayNames {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			wanted = append(wanted, name)
		}
	}
	return wanted
}

func windowsInstalledApplications() []windowsInstalledApplication {
	const uninstallPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	var applications []windowsInstalledApplication
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
				displayName := strings.ToLower(strings.TrimSpace(registryString(root, entry, "DisplayName", view)))
				if displayName == "" {
					continue
				}
				location := strings.TrimSpace(registryString(root, entry, "InstallLocation", view))
				if location == "" {
					if executable := commandExecutable(registryString(root, entry, "DisplayIcon", view)); executable != "" {
						location = filepath.Dir(executable)
					} else if executable := commandExecutable(registryString(root, entry, "UninstallString", view)); executable != "" {
						location = executable
					}
				}
				applications = append(applications, windowsInstalledApplication{
					displayName: displayName, location: location,
				})
			}
		}
	}
	return applications
}

func matchesInstalledDisplayName(displayName string, wanted []string) bool {
	for _, name := range wanted {
		if displayName == name || strings.HasPrefix(displayName, name+" ") ||
			strings.HasPrefix(displayName, name+"(") || strings.HasPrefix(displayName, name+"-") {
			return true
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
	lower := strings.ToLower(command)
	for _, extension := range []string{".exe", ".cmd", ".bat", ".com"} {
		if end := strings.Index(lower, extension); end >= 0 {
			return strings.TrimSpace(command[:end+len(extension)])
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
