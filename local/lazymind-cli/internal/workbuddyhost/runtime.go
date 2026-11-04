package workbuddyhost

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/workflowpresentation"
)

//go:embed assets/connector.mjs
var assets embed.FS

// Supervise belongs to Assistant Bridge, not to any MCP connection. Configuration
// or account changes stop the old worker before a replacement is launched.
func Supervise(ctx context.Context, home, bridgeURL string, account func() (string, error)) {
	bundle, err := assets.ReadFile("assets/connector.mjs")
	if err != nil {
		return
	}
	sum := sha256.Sum256(bundle)
	script := filepath.Join(home, "workbuddy-workflow", "connector-"+hex.EncodeToString(sum[:8])+".mjs")
	var worker *exec.Cmd
	var stop context.CancelFunc
	var done chan error
	var fingerprint string
	startupMigrationChecked := false
	cleanup := func() {
		if stop != nil {
			stop()
			<-done
		}
		worker = nil
		stop = nil
		done = nil
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
		current := config.Node + "|" + config.PairingFile + "|" + strconv.Itoa(config.CDPPort) + "|" + scope + "|" + pair.Token + "|" + string(definition)
		if scopeErr != nil || configErr != nil || !pair.Enabled || !integrationstate.Allowed(home, "workbuddy") {
			if worker != nil {
				cleanup()
			}
		} else {
			if !startupMigrationChecked {
				// Retry a failed migration on the next Bridge startup, not every
				// supervisor tick. Temporary account failures never reach this path.
				startupMigrationChecked = true
				if err := migrateDesktopStartup(ctx, home, scope); err != nil {
					log.Printf("WorkBuddy startup migration: %v", err)
				}
			}
			if worker != nil && current != fingerprint {
				cleanup()
			}
			if worker == nil {
				var node string
				var nodeErr error
				if connection != nil && connection.Overrides["LAZYMIND_NODE_BIN"] != "" {
					node, nodeErr = agentexec.CompatibleNode(ctx, connection.Overrides["LAZYMIND_NODE_BIN"])
				} else {
					node, nodeErr = findNode(ctx, pair.Profile, config.Node)
				}
				if nodeErr != nil {
					log.Printf("WorkBuddy runtime: %v", nodeErr)
				} else {
					config.Node = node
					if err := writeAtomic(script, bundle); err == nil {
						logDir := filepath.Join(home, "logs")
						_ = os.MkdirAll(logDir, 0700)
						log, err := os.OpenFile(filepath.Join(logDir, "workbuddy-workflow.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
						if err == nil {
							var lifetime context.Context
							lifetime, stop = context.WithCancel(ctx)
							cmd := exec.CommandContext(lifetime, config.Node, script, "--pairing-file", config.PairingFile,
								"--bridge-url", bridgeURL, "--cdp-port", strconv.Itoa(config.CDPPort), "--parent-pid", strconv.Itoa(os.Getpid()))
							cmd.Stdout, cmd.Stderr = log, log
							cmd.Cancel = func() error {
								if runtime.GOOS == "windows" {
									return cmd.Process.Kill()
								}
								return cmd.Process.Signal(os.Interrupt)
							}
							cmd.WaitDelay = 3 * time.Second
							if err := cmd.Start(); err == nil {
								worker = cmd
								fingerprint = current
								done = make(chan error, 1)
								ch := done
								go func() { ch <- cmd.Wait(); log.Close() }()
							} else {
								stop()
								stop = nil
								log.Close()
							}
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			stop()
			worker = nil
			stop = nil
			done = nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	ready, err := desktopStatus(ctx, config.CDPPort)
	if err != nil || !ready {
		return err
	}
	bundle, err := assets.ReadFile("assets/connector.mjs")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(bundle)
	script := filepath.Join(home, "workbuddy-workflow", "connector-"+hex.EncodeToString(sum[:8])+".mjs")
	if err := writeAtomic(script, bundle); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, config.Node, script, "--pairing-file", config.PairingFile, "--cdp-port", strconv.Itoa(config.CDPPort), "--cleanup-only")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("WorkBuddy is disabled but its panel cleanup failed; retry disconnect: %w", err)
	}
	return nil
}
