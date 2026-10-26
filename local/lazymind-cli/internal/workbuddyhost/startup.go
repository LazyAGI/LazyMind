package workbuddyhost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"lazymind/agentconnector/internal/localfile"
)

const desktopDebugPortEnv = "WORKBUDDY_REMOTE_DEBUGGING_PORT"

type startupEnvironment interface {
	Get() (string, error)
	Set(string) error
	Unset() error
}

type startupOwnership struct {
	Previous string `json:"previous"`
	Managed  string `json:"managed"`
}

// WorkBuddy reads this variable itself before Electron startup. Keep ownership
// outside the app bundle so upgrades do not erase it and disable can restore it.
func updateStartupEnvironment(home string, port int, enabled bool, env startupEnvironment) error {
	path := filepath.Join(home, "workbuddy-workflow", "startup-environment.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	unlock, err := localfile.Lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	var owned startupOwnership
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && json.Unmarshal(body, &owned) != nil {
		return fmt.Errorf("invalid WorkBuddy startup ownership")
	}
	if !enabled && owned.Managed == "" {
		return nil
	}
	current, err := env.Get()
	if err != nil {
		return err
	}
	if !enabled {
		if current == owned.Managed {
			if owned.Previous == "" {
				err = env.Unset()
			} else {
				err = env.Set(owned.Previous)
			}
			if err != nil {
				return err
			}
		} else if err := refreshStartupEnvironment(env); err != nil {
			// A prior restore may have written the registry but failed to
			// notify Explorer. Keep ownership until publication succeeds.
			return err
		}
		return os.Remove(path)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid WorkBuddy startup port")
	}
	desired := strconv.Itoa(port)
	if current == desired {
		// A previous write may have succeeded while notifying the desktop
		// failed. Retry publication without taking ownership of user settings.
		return refreshStartupEnvironment(env)
	}
	// An empty value is expected after logout; reinstall our setting then. Do
	// not replace an environment value someone else changed while enabled.
	if owned.Managed != "" && current != "" && current != owned.Managed {
		return fmt.Errorf("WorkBuddy startup environment changed outside LazyMind")
	}
	if owned.Managed == "" {
		owned.Previous = current
	}
	owned.Managed = desired
	body, _ = json.Marshal(owned)
	if err := writeAtomic(path, body); err != nil {
		return err
	}
	return env.Set(desired)
}

func refreshStartupEnvironment(env startupEnvironment) error {
	if publisher, ok := env.(interface{ Refresh() error }); ok {
		return publisher.Refresh()
	}
	return nil
}
