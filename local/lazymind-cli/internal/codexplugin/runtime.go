package codexplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

// StartWorker keeps all worker output off MCP stdout and ties its lifetime to
// the MCP connection. Multiple MCP processes elect one dispatcher via its lock.
func StartWorker(ctx context.Context, home string) (func(), error) {
	resolve := func() (node, binary, pairing, profile string, err error) {
		c, err := connectorconfig.Current()
		if err != nil {
			return "", "", "", "", err
		}
		if c == nil {
			node, binary, pairing, profile = connectorconfig.Value("LAZYMIND_NODE_BIN"), connectorconfig.Value("LAZYMIND_CODEX_BIN"), connectorconfig.Value("LAZYMIND_WORKFLOW_PAIRING_FILE"), connectorconfig.Value("CODEX_HOME")
		} else {
			if c.Provider != "codex" || !c.Controlled || c.Home != home {
				return "", "", "", "", errors.New("connection does not belong to this Codex worker")
			}
			record, readErr := Read()
			if readErr != nil {
				return "", "", "", "", readErr
			}
			if record.Home != home || record.Profile != c.Profile {
				return "", "", "", "", errors.New("Codex installation ownership changed; reconnect")
			}
			pairing, profile = c.PairingFile, c.Profile
			node, err = findNode(ctx, record.Node)
			if err != nil {
				return "", "", "", "", err
			}
			if override := connectorconfig.Value("LAZYMIND_CODEX_BIN"); override != "" {
				binary, err = agentexec.ResolveExecutable(override)
			} else if configured, found, bindErr := agentexec.ExecutableBinding(agentexec.CodexCLI); bindErr != nil {
				return "", "", "", "", bindErr
			} else if found {
				binary, err = agentexec.ResolveExecutable(configured)
			} else {
				binary, err = agentexec.ResolveExecutable(record.Binary)
				if err != nil {
					binary, err = agentexec.FindExecutable("", []string{"codex"})
				}
			}
			if err != nil {
				return "", "", "", "", fmt.Errorf("resolve Codex executable: %w", err)
			}
		}
		if !filepath.IsAbs(node) || !filepath.IsAbs(binary) || !filepath.IsAbs(pairing) || !filepath.IsAbs(profile) {
			return "", "", "", "", errors.New("LazyMind Codex plugin is not configured; reconnect Codex in LazyMind")
		}
		store, err := credentials.NewStore(home, "")
		if err != nil {
			return "", "", "", "", err
		}
		account, err := store.AccountScope()
		if err != nil {
			return "", "", "", "", err
		}
		body, err := os.ReadFile(pairing)
		if err != nil {
			return "", "", "", "", err
		}
		var pair workflowhost.Pairing
		if json.Unmarshal(body, &pair) != nil || pair.Provider != "codex" || pair.Profile != profile {
			return "", "", "", "", errors.New("invalid Codex pairing")
		}
		if _, err = workflowhost.Verify(home, pair.ConnectorID, pair.Token, account); err != nil {
			return "", "", "", "", err
		}
		return node, binary, pairing, profile, nil
	}
	bundle, err := assets.ReadFile("assets/dispatcher.mjs")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bundle)
	script := filepath.Join(home, "codex-workflow", "dispatcher-"+hex.EncodeToString(sum[:8])+".mjs")
	if _, err := os.Stat(script); errors.Is(err, os.ErrNotExist) {
		if err := writeAtomic(script, bundle); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	logPath := filepath.Join(home, "logs", "codex-workflow.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	state := func() (string, error) {
		c, err := connectorconfig.Current()
		if err != nil {
			return "", err
		}
		store, err := credentials.NewStore(home, "")
		if err != nil {
			return "", err
		}
		account, err := store.AccountScope()
		if err != nil {
			return "", err
		}
		pairing, err := os.ReadFile(connectorconfig.Value("LAZYMIND_WORKFLOW_PAIRING_FILE"))
		if err != nil {
			return "", err
		}
		var record Installation
		var binding string
		if c != nil {
			record, err = Read()
			if err != nil {
				return "", err
			}
			binding, _, err = agentexec.ExecutableBinding(agentexec.CodexCLI)
			if err != nil {
				return "", err
			}
		}
		data, _ := json.Marshal([]any{c, record, binding, account, string(pairing)})
		return fmt.Sprintf("%x", sha256.Sum256(data)), nil
	}
	lifetime, cancel := context.WithCancel(ctx)
	var stopChild context.CancelFunc
	var generation string
	launch := func() (*exec.Cmd, error) {
		before, err := state()
		if err != nil {
			return nil, err
		}
		node, binary, pairing, profile, err := resolve()
		if err != nil {
			return nil, err
		}
		bridgeURL, err := connectorconfig.URL("LAZYMIND_ASSISTANT_BRIDGE_URL", connectorconfig.DefaultBridgeURL)
		if err != nil {
			return nil, err
		}
		args := []string{script, "--bridge-url", bridgeURL, "--managed", "--parent-pid", strconv.Itoa(os.Getpid()), "--codex-bin", binary, "--codex-home", profile, "--pairing-file", pairing}
		next, err := state()
		if err != nil {
			return nil, err
		}
		if next != before {
			return nil, errors.New("connection changed during worker startup; retry")
		}
		child, childCancel := context.WithCancel(lifetime)
		cmd := exec.CommandContext(child, node, args...)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.WaitDelay = 5 * time.Second
		cmd.Cancel = func() error {
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
		if err := cmd.Start(); err != nil {
			childCancel()
			return nil, err
		}
		stopChild, generation = childCancel, next
		return cmd, nil
	}
	first, err := launch()
	if err != nil {
		cancel()
		log.Close()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer log.Close()
		current := first
		for {
			exited := make(chan error, 1)
			go func(cmd *exec.Cmd) { exited <- cmd.Wait() }(current)
			ticker := time.NewTicker(2 * time.Second)
			var err error
		running:
			for {
				select {
				case err = <-exited:
					break running
				case <-lifetime.Done():
					stopChild()
					err = <-exited
					break running
				case <-ticker.C:
					next, stateErr := state()
					if stateErr != nil || next != generation {
						stopChild()
						err = <-exited
						break running
					}
				}
			}
			ticker.Stop()
			stopChild()
			if lifetime.Err() != nil {
				return
			}
			fmt.Fprintf(log, "Workflow dispatcher exited (%v); restarting in 3s\n", err)
			select {
			case <-lifetime.Done():
				return
			case <-time.After(3 * time.Second):
			}
			for {
				current, err = launch()
				if err == nil {
					break
				}
				fmt.Fprintf(log, "Workflow dispatcher restart failed: %v\n", err)
				select {
				case <-lifetime.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
