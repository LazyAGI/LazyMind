package workbuddyhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"lazymind/agentconnector/internal/desktopsetup"
	"lazymind/agentconnector/internal/workflowhost"
)

func StartupHealth(ctx context.Context, home string, port int) string {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := checkDesktopStartup(bounded, home, port); err != nil {
		return "startup_unavailable"
	}
	application, err := desktopApplication()
	if err != nil {
		return "startup_unavailable"
	}
	running, err := desktopsetup.Running(bounded, application)
	if err != nil {
		return "host_unavailable"
	}
	if !running {
		return "waiting_for_app"
	}
	ready, err := desktopStatus(bounded, port)
	if err != nil || !ready || !desktopsetup.ListenerMatches(bounded, application, port) {
		return "cdp_unavailable"
	}
	return ""
}

// Explicit local disconnect remains available while signed out. It never grants
// account access or edits another installation's pairing.
func DisableLocal(home string) error {
	body, err := os.ReadFile(ConfigPath(home))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		var config Config
		if json.Unmarshal(body, &config) != nil {
			return fmt.Errorf("invalid WorkBuddy configuration")
		}
		body, err = os.ReadFile(config.PairingFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			var pair workflowhost.Pairing
			if json.Unmarshal(body, &pair) != nil || pair.Provider != "workbuddy" {
				return fmt.Errorf("invalid WorkBuddy pairing")
			}
			if err = workflowhost.Disable(home, pair.ConnectorID); err != nil {
				return err
			}
		}
		if err = configureDesktopStartup(context.Background(), home, 0, false); err != nil {
			return err
		}
		return removePresentation(home, config)
	}
	return configureDesktopStartup(context.Background(), home, 0, false)
}
