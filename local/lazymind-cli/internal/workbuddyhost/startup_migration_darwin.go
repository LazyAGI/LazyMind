package workbuddyhost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"lazymind/agentconnector/internal/desktopsetup"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/localfile"
)

func migrateDesktopStartup(ctx context.Context, home, account string) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return migrateLoginEnvironment(bounded, home, account, launchEnvironment{bounded})
}

// Older installations only set the current login's environment. Install their
// persistent login job once, without turning ordinary supervision into repair.
func migrateLoginEnvironment(ctx context.Context, home, account string, env startupEnvironment) error {
	unlock, err := desktopsetup.Lock(home, "workbuddy")
	if err != nil {
		return err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	choice, err := integrationstate.Read(home, "workbuddy")
	if err != nil || choice != nil && !*choice {
		return err
	}
	dir := filepath.Join(home, "workbuddy-workflow")
	marker := filepath.Join(dir, "startup-migration-v1")
	state, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		switch string(state) {
		case "complete\n":
			return nil
		case "pending\n":
			// Resume an interrupted migration even if it already wrote the job.
		default:
			return fmt.Errorf("invalid WorkBuddy startup migration state")
		}
	} else {
		if _, err := os.Stat(filepath.Join(dir, "login-files.json")); err == nil {
			// Explicit installation (including disable) already uses the new format.
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if choice != nil {
			// New installs persist a choice before setup; a failed new install is
			// not a legacy installation to silently finish in the background.
			return nil
		}
	}
	// Revalidate under the setup lock: account failures and disabled pairings
	// must not recreate startup resources, even with an unfinished journal.
	config, _, err := Load(home, account)
	if err != nil {
		return err
	}
	if err := localfile.Write(marker, []byte("pending\n"), 0600); err != nil {
		return err
	}
	if err := configureLoginEnvironment(ctx, home, config.CDPPort, true); err != nil {
		return err
	}
	if err := updateStartupEnvironment(home, config.CDPPort, true, env); err != nil {
		return err
	}
	return localfile.Write(marker, []byte("complete\n"), 0600)
}
