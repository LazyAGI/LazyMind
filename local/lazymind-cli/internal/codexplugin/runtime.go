package codexplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// StartWorker keeps all worker output off MCP stdout and ties its lifetime to
// the MCP connection. Multiple MCP processes elect one dispatcher via its lock.
func StartWorker(ctx context.Context, home string) (func(), error) {
	node, binary, pairing, profile := os.Getenv("LAZYMIND_NODE_BIN"), os.Getenv("LAZYMIND_CODEX_BIN"), os.Getenv("LAZYMIND_WORKFLOW_PAIRING_FILE"), os.Getenv("CODEX_HOME")
	if !filepath.IsAbs(node) || !filepath.IsAbs(binary) || !filepath.IsAbs(pairing) || !filepath.IsAbs(profile) {
		return nil, errors.New("LazyMind Codex plugin is not configured; reconnect Codex in LazyMind")
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
	lifetime, cancel := context.WithCancel(ctx)
	args := []string{script, "--managed", "--parent-pid", strconv.Itoa(os.Getpid()), "--codex-bin", binary, "--codex-home", profile, "--pairing-file", pairing}
	launch := func() (*exec.Cmd, error) {
		cmd := exec.CommandContext(lifetime, node, args...)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.WaitDelay = 5 * time.Second
		cmd.Cancel = func() error {
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
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
			err := current.Wait()
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
