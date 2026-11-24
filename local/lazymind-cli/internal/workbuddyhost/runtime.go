package workbuddyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowpresentation"
)

// Supervise belongs to Assistant Bridge, not to any MCP connection. Configuration
// or account changes stop the old worker before a replacement is launched.
func Supervise(ctx context.Context, home, bridgeURL string, account func() (string, error)) {
	var worker *workflowconnector.Worker
	var done <-chan error
	var running Config
	var fingerprint, lastFailure string
	startupMigrationChecked := false
	cleanup := func() {
		if worker != nil {
			graceful := worker.Stop()
			_ = os.Remove(running.PairingFile + ".workbuddy-status.json")
			if !graceful {
				if err := cleanupPresentation(home, running, "--reload-cleanup"); err != nil {
					log.Printf("WorkBuddy Connector cleanup: %v", err)
				}
			}
		}
		worker, done = nil, nil
	}
	defer cleanup()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		scope, scopeErr := account()
		config, pair, configErr := Load(home, scope)
		connection, connectionErr := connectorconfig.ForProvider(home, "workbuddy", "")
		if connectionErr != nil {
			configErr = connectionErr
		}
		if config.ConnectionFile != "" && (connection == nil || config.ConnectionFile != connectorconfig.Path(home, "workbuddy", "")) {
			configErr = errors.New("WorkBuddy connection configuration is missing; reconnect")
		}
		definition, _ := json.Marshal(connection)
		if scopeErr != nil || configErr != nil || !pair.Enabled || !registered(home) {
			cleanup()
			lastFailure = ""
		} else {
			if !startupMigrationChecked {
				// Retry a failed migration on the next Bridge startup, not every
				// supervisor tick. Temporary account failures never reach this path.
				startupMigrationChecked = true
				if err := migrateDesktopStartup(ctx, home, scope); err != nil {
					log.Printf("WorkBuddy startup migration: %v", err)
				}
			}
			installed, installErr := workflowconnector.Resolve(home, "workbuddy")
			reload := workflowconnector.ReloadRequest(home, "workbuddy")
			current := config.Node + "|" + config.PairingFile + "|" + strconv.Itoa(config.CDPPort) + "|" + scope + "|" + pair.Token + "|" + string(definition) + "|" + installed.Release + "|" + reload
			if done != nil && (current != fingerprint || installErr != nil) {
				cleanup()
			}
			if done == nil && installErr == nil {
				if connection != nil && connection.Overrides["LAZYMIND_NODE_BIN"] != "" {
					config.Node, installErr = agentexec.CompatibleNode(ctx, connection.Overrides["LAZYMIND_NODE_BIN"])
				} else {
					config.Node, installErr = findNode(ctx, pair.Profile, config.Node)
				}
				if installErr == nil {
					worker, installErr = installed.StartWorker(ctx, home, reload, func(child context.Context, stopFile string) *exec.Cmd {
						cmd := backgroundCommand(child, config.Node, installed.EntryPath(), "--pairing-file", config.PairingFile,
							"--bridge-url", bridgeURL, "--cdp-port", strconv.Itoa(config.CDPPort), "--parent-pid", strconv.Itoa(os.Getpid()), "--stop-file", stopFile)
						cmd.Env = agentexec.SafeEnvironment()
						return cmd
					})
					if worker != nil {
						running, fingerprint, done = config, current, worker.Done
					}
				}
			}
			if installErr != nil && installErr.Error() != lastFailure {
				lastFailure = installErr.Error()
				log.Printf("WorkBuddy Connector: %v", installErr)
			} else if installErr == nil {
				lastFailure = ""
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			_ = os.Remove(running.PairingFile + ".workbuddy-status.json")
			worker, done = nil, nil
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		case <-ticker.C:
		}
	}
}

func PresentationHTML(frontendURL string) []byte {
	return workflowpresentation.HTML(frontendURL, "host")
}

// A stopped Bridge cannot remove an old renderer mount. Disconnect therefore
// performs one bounded cleanup, after the worker has relinquished its lock.
func removePresentation(home string, config Config) error {
	return cleanupPresentation(home, config, "--cleanup-only")
}

func cleanupPresentation(home string, config Config, mode string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	ready, err := desktopStatus(ctx, config.CDPPort)
	if err != nil || !ready {
		return err
	}
	installed, err := workflowconnector.Resolve(home, "workbuddy")
	if err != nil {
		return err
	}
	cmd := backgroundCommand(ctx, config.Node, installed.EntryPath(), "--pairing-file", config.PairingFile, "--cdp-port", strconv.Itoa(config.CDPPort), mode)
	cmd.Env = agentexec.SafeEnvironment()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("WorkBuddy Connector panel cleanup: %w", err)
	}
	return nil
}
