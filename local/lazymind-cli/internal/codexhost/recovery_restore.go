package codexhost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/localfile"
)

var recoveryThread = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type recoveryThreadKey struct{}

func threadLink(thread string) (string, error) {
	if !recoveryThread.MatchString(thread) {
		return "", errors.New("invalid Codex conversation ID")
	}
	return "codex://threads/" + thread, nil
}

func desktopLaunchArguments(ctx context.Context, port int) []string {
	args := []string{"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=" + strconv.Itoa(port)}
	if thread, ok := ctx.Value(recoveryThreadKey{}).(string); ok {
		if link, err := threadLink(thread); err == nil {
			args = append(args, link)
		}
	}
	return args
}

var recoveryOpenThread = openDesktopThread
var recoveryDeliver = deliverRecovery

// Keep restoration under the same account/pairing revocation guard as restart.
func ReconnectRequest(ctx context.Context, home, id string, progress func(string)) error {
	if err := os.MkdirAll(filepath.Join(home, "codex-workflow"), 0700); err != nil {
		return err
	}
	// Also serialize distinct CLI children, not just clicks in one Electron window.
	unlock, err := localfile.Lock(filepath.Join(home, "codex-workflow", "recovery.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := CurrentRecovery(ctx, home)
	if err != nil {
		return err
	}
	if current.Request == nil || current.Request.ID != id || !current.Request.CanRestart {
		return errors.New("Codex recovery request expired; retry the original operation")
	}
	if current.Request.ThreadID != "" {
		if _, err := threadLink(current.Request.ThreadID); err != nil {
			return err
		}
		ctx = context.WithValue(ctx, recoveryThreadKey{}, current.Request.ThreadID)
	}
	return reconnect(ctx, home, progress, func(ctx context.Context) error {
		opened := ""
		for {
			current, err := CurrentRecovery(ctx, home)
			if err != nil {
				return err
			}
			if current.Request == nil || current.Request.ID != id {
				return errors.New("Codex recovery request expired")
			}
			request := current.Request
			open := func(thread string) error {
				if opened == thread {
					return nil
				}
				if _, err := threadLink(thread); err != nil {
					return err
				}
				if err := waitForConnection(ctx, home); err != nil {
					return err
				}
				application, err := startupApplication()
				if err != nil {
					return err
				}
				reportRecovery(ctx, "restoring")
				if err := recoveryOpenThread(ctx, application, thread); err != nil {
					return err
				}
				opened = thread
				return nil
			}
			for _, intent := range request.Intents {
				if intent.Accepted {
					continue
				}
				if err := open(intent.ThreadID); err != nil {
					return err
				}
				if err := recoveryDeliver(ctx, home, id, intent); err != nil {
					return err
				}
				if err := AcceptRecoveryIntent(ctx, home, id, intent.ID); err != nil {
					return err
				}
			}
			if len(request.Intents) == 0 && request.ThreadID != "" {
				if err := open(request.ThreadID); err != nil {
					return err
				}
				// Control notifications remain in their existing dispatcher queue.
				if err := recoveryDeliver(ctx, home, id, RecoveryIntent{ThreadID: request.ThreadID}); err != nil {
					return err
				}
			}
			// Complete is conditional: a concurrently queued request is never lost.
			if err := CompleteRecovery(ctx, home, id); err == nil {
				return nil
			}
			latest, err := CurrentRecovery(ctx, home)
			if err != nil {
				return err
			}
			if latest.Request == nil || latest.Request.ID != id {
				return errors.New("Codex recovery request expired")
			}
			pending := false
			for _, intent := range latest.Request.Intents {
				pending = pending || !intent.Accepted
			}
			if !pending {
				return errors.New("cannot finish Codex recovery")
			}
		}
	})
}

func deliverRecovery(ctx context.Context, home, request string, intent RecoveryIntent) error {
	store, err := credentials.NewStore(home, "")
	if err != nil {
		return err
	}
	scope, err := store.AccountScope()
	if err != nil {
		return err
	}
	c, _, err := Load(home, scope)
	if err != nil {
		return err
	}
	bundle, err := codexplugin.DispatcherBundle()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(bundle)
	script := filepath.Join(home, "codex-workflow", "recover-"+hex.EncodeToString(sum[:8])+".mjs")
	if err := localfile.Write(script, bundle, 0600); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"request_id": request, "intent": intent})
	if err != nil {
		return err
	}
	address, err := connectorconfig.BridgeAddress()
	if err != nil {
		return err
	}
	command := backgroundCommand(ctx, c.Node, script, "--recover-intent", "--pairing-file", c.PairingFile, "--cdp-port", strconv.Itoa(c.Port), "--bridge-url", "http://"+address)
	command.Env = agentexec.SafeEnvironment()
	command.Stdin = bytes.NewReader(body)
	command.WaitDelay = time.Second
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	accepted := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event struct {
			Stage    string `json:"stage"`
			Accepted bool   `json:"accepted"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		accepted = accepted || event.Accepted
		switch event.Stage {
		case "waiting-chat", "waiting-idle", "showing-panel", "resuming":
			reportRecovery(ctx, event.Stage)
		}
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("restore Codex conversation: %s: %w", stderr.String(), err)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !accepted {
		return errors.New("Codex did not confirm the recovery handoff")
	}
	return nil
}
