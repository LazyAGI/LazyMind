package agentexec

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type DesktopApplication struct {
	BindingTarget   BindingTarget
	ExecutableNames []string
	Protocols       []string
	DisplayNames    []string
	StatePaths      []string
}

type DesktopApplicationState struct {
	Installed   bool
	Initialized bool
}

func InspectDesktopApplication(spec DesktopApplication) (DesktopApplicationState, error) {
	state := DesktopApplicationState{}
	initialized := anyPathExists(spec.StatePaths)
	if spec.BindingTarget != "" {
		path, found, err := ExecutableBinding(spec.BindingTarget)
		if err != nil {
			return state, err
		}
		if found {
			if _, err := ResolveDesktopApplication(path); err == nil {
				state.Installed = true
			}
		}
	}
	if !state.Installed {
		state.Installed = platformDesktopInstalled(spec, initialized)
	}
	state.Initialized = state.Installed && initialized
	return state, nil
}

func ResolveDesktopApplication(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("desktop application path is empty")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if runtime.GOOS == "darwin" && strings.EqualFold(filepath.Ext(abs), ".app") {
			return filepath.Clean(abs), nil
		}
		return "", fmt.Errorf("%s is not a desktop application", abs)
	}
	return ResolveExecutable(abs)
}

func anyPathExists(paths []string) bool {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}
