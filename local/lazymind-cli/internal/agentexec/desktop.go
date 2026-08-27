package agentexec

import (
	"os"
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
	state := DesktopApplicationState{Initialized: anyPathExists(spec.StatePaths)}
	if spec.BindingTarget != "" {
		path, found, err := ExecutableBinding(spec.BindingTarget)
		if err != nil {
			return state, err
		}
		if found {
			if _, err := ResolveExecutable(path); err == nil {
				state.Installed = true
			}
		}
	}
	if !state.Installed {
		state.Installed = platformDesktopInstalled(spec, state.Initialized)
	}
	return state, nil
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
