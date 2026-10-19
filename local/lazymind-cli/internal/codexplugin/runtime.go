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

	"lazymind/agentconnector/internal/executorpolicy"
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
	policy, err := executorpolicy.New(home)
	if err != nil {
		log.Close()
		return nil, err
	}
	enabled, err := policy.Enabled("codex")
	if err != nil {
		log.Close()
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	args := []string{script, "--managed", "--parent-pid", strconv.Itoa(os.Getpid()), "--codex-bin", binary, "--codex-home", profile, "--pairing-file", pairing}
	type worker struct {
		cancel context.CancelFunc
		exited chan error
	}
	launch := func() (*worker, error) {
		workerContext, stopWorker := context.WithCancel(lifetime)
		cmd := exec.CommandContext(workerContext, node, args...)
		self, selfErr := os.Executable()
		if selfErr != nil {
			stopWorker()
			return nil, selfErr
		}
		cmd.Env = append(os.Environ(), "LAZYMIND_RUNTIME_HELPER="+self)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.WaitDelay = 5 * time.Second
		cmd.Cancel = func() error {
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
		if err := cmd.Start(); err != nil {
			stopWorker()
			return nil, err
		}
		current := &worker{cancel: stopWorker, exited: make(chan error, 1)}
		go func() { current.exited <- cmd.Wait() }()
		return current, nil
	}
	var current *worker
	if enabled {
		current, err = launch()
		if err != nil {
			cancel()
			log.Close()
			return nil, err
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer log.Close()
		defer func() {
			if current != nil {
				current.cancel()
				<-current.exited
			}
		}()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		var retryAt time.Time
		for {
			var exited <-chan error
			if current != nil {
				exited = current.exited
			}
			select {
			case <-lifetime.Done():
				return
			case err := <-exited:
				current.cancel()
				current = nil
				if lifetime.Err() != nil {
					return
				}
				fmt.Fprintf(log, "Workflow dispatcher exited (%v); retrying in 3s if enabled\n", err)
				retryAt = time.Now().Add(3 * time.Second)
			case <-ticker.C:
				enabled, err := policy.Enabled("codex")
				if err != nil {
					fmt.Fprintf(log, "Workflow policy unavailable; stopping dispatcher: %v\n", err)
					enabled = false
				}
				if !enabled {
					if current != nil {
						current.cancel()
						<-current.exited
						current = nil
					}
					retryAt = time.Time{}
					continue
				}
				if current == nil && !time.Now().Before(retryAt) {
					current, err = launch()
					if err != nil {
						fmt.Fprintf(log, "Workflow dispatcher restart failed: %v\n", err)
						retryAt = time.Now().Add(3 * time.Second)
					}
				}
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
