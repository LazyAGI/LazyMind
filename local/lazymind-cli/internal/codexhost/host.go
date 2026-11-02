// Package codexhost owns the CDP connector under the existing Assistant Bridge.
package codexhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/workflowhost"
)

type Config struct {
	Node        string `json:"node"`
	PairingFile string `json:"pairing_file"`
	Port        int    `json:"cdp_port"`
}
type Status struct {
	Connector    string `json:"connector_id"`
	CheckedAt    int64  `json:"checked_at"`
	Ready        bool   `json:"ready"`
	Issue        string `json:"issue"`
	Presentation struct {
		Status     string `json:"status"`
		PageStatus string `json:"page_status"`
	} `json:"presentation"`
}

func configPath(home string) string { return filepath.Join(home, "codex-workflow", "config.json") }
func Configure(home, node, pairing string) error {
	c := Config{Node: node, PairingFile: pairing, Port: 9334}
	if previous, err := readConfig(home); err == nil {
		c.Port = previous.Port
	}
	if err := os.MkdirAll(filepath.Dir(configPath(home)), 0700); err != nil {
		return err
	}
	body, _ := json.Marshal(c)
	return os.WriteFile(configPath(home), body, 0600)
}
func readConfig(home string) (Config, error) {
	var c Config
	body, err := os.ReadFile(configPath(home))
	if err != nil {
		return c, err
	}
	if json.Unmarshal(body, &c) != nil || !filepath.IsAbs(c.Node) || !filepath.IsAbs(c.PairingFile) || c.Port < 1 || c.Port > 65535 {
		return c, errors.New("invalid Codex CDP configuration")
	}
	return c, nil
}
func Load(home, account string) (Config, workflowhost.Pairing, error) {
	c, err := readConfig(home)
	var p workflowhost.Pairing
	if err != nil {
		return c, p, err
	}
	body, err := os.ReadFile(c.PairingFile)
	if err != nil {
		return c, p, err
	}
	if json.Unmarshal(body, &p) != nil || p.Provider != "codex" {
		return c, p, errors.New("invalid Codex pairing")
	}
	p, err = workflowhost.Verify(home, p.ConnectorID, p.Token, account)
	return c, p, err
}
func ReadStatus(home, account string) (Status, error) {
	c, p, err := Load(home, account)
	var s Status
	if err != nil {
		return s, err
	}
	body, err := os.ReadFile(c.PairingFile + ".codex-status.json")
	if err != nil {
		return s, err
	}
	if json.Unmarshal(body, &s) != nil || s.Connector != p.ConnectorID || time.Now().UnixMilli()-s.CheckedAt > 12000 || s.CheckedAt > time.Now().UnixMilli() {
		return s, errors.New("Codex connector is offline")
	}
	return s, nil
}
func Ready(home, account string) bool { s, e := ReadStatus(home, account); return e == nil && s.Ready }

var localChatRoute = regexp.MustCompile(`(?i)^/local/[a-f0-9-]{36}$`)

func DesktopReady(ctx context.Context, port int) (bool, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/json/list", nil)
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("CDP redirect refused") }}
	response, err := client.Do(req)
	if err != nil {
		if errors.Is(err, connectionRefused) {
			return false, nil
		}
		return false, fmt.Errorf("Codex CDP endpoint is unavailable: %w", err)
	}
	defer response.Body.Close()
	var targets []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if response.StatusCode != 200 || json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&targets) != nil {
		return false, errors.New("Codex CDP port is occupied by another service")
	}
	count := 0
	for _, target := range targets {
		u, e := url.Parse(target.URL)
		if e == nil && target.Type == "page" && u.Scheme == "app" && u.Host == "-" &&
			(u.Path == "/index.html" && (!u.Query().Has("initialRoute") || localChatRoute.MatchString(u.Query().Get("initialRoute"))) || u.Path == "/detached-window.html" && (u.Query().Get("initialRoute") == "" || u.Query().Get("initialRoute") == "/detached-window")) {
			count++
		}
	}
	if count == 0 {
		return false, errors.New("Codex chat window is missing")
	}
	return true, nil
}

var prepareDesktopLaunch = launchDesktop

func PrepareDesktop(ctx context.Context, home string) error {
	if !integrationstate.Allowed(home, "codex") {
		return connectionIssue("CODEX_INTEGRATION_DISABLED", "Codex connection is disabled", false)
	}
	c, err := readConfig(home)
	if err != nil {
		return err
	}
	application, err := startupApplication()
	if err != nil {
		return err
	}
	ready, err := startupDesktopReady(ctx, c.Port)
	if err != nil {
		return err
	}
	if ready && !startupListenerMatches(ctx, application, c.Port) {
		return errors.New("CODEX_CDP_UNAVAILABLE: the debugging port is not owned by Codex; resolve the port conflict before reconnecting")
	}
	if !ready {
		reportRecovery(ctx, "closing")
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := prepareDesktopLaunch(ctx, application, c.Port); err != nil {
			return err
		}
	}
	reportRecovery(ctx, "connecting")
	return waitForConnection(ctx, home)
}

// Startup can briefly invalidate a successful probe while windows are restored.
// Retry only read-only checks; never restart or duplicate a recovery handoff.
func waitForConnection(ctx context.Context, home string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		connectionErr := CheckConnection(ctx, home)
		if connectionErr == nil {
			return nil
		}
		var issue *ConnectionIssue
		if errors.As(connectionErr, &issue) && (issue.Code == "CODEX_INTEGRATION_DISABLED" || issue.Code == "CODEX_CONFIGURATION_UNAVAILABLE") {
			return connectionErr
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Codex connection did not become ready: %w: %v", ctx.Err(), connectionErr)
		case <-ticker.C:
		}
	}
}
func Supervise(ctx context.Context, home, bridgeURL string, account func() (string, error)) {
	var current *exec.Cmd
	var cancel context.CancelFunc
	var done chan error
	var fingerprint string
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			_ = CleanupPresentation(home)
		}
		current = nil
		cancel = nil
		done = nil
	}
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		scope, scopeErr := account()
		c, p, err := Load(home, scope)
		next := c.Node + "|" + c.PairingFile + "|" + strconv.Itoa(c.Port) + "|" + scope + "|" + p.Token
		if scopeErr != nil || err != nil || !p.Enabled || !integrationstate.Allowed(home, "codex") {
			if current != nil {
				stop()
			}
		} else {
			if current != nil && fingerprint != next {
				stop()
			}
			if current == nil {
				bundle, loadErr := codexplugin.DispatcherBundle()
				if loadErr == nil {
					sum := sha256.Sum256(bundle)
					script := filepath.Join(home, "codex-workflow", "connector-"+hex.EncodeToString(sum[:8])+".mjs")
					if os.MkdirAll(filepath.Dir(script), 0700) == nil && os.WriteFile(script, bundle, 0600) == nil {
						_ = os.MkdirAll(filepath.Join(home, "logs"), 0700)
						log, e := os.OpenFile(filepath.Join(home, "logs", "codex-workflow.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
						if e == nil {
							child, closeChild := context.WithCancel(ctx)
							cmd := backgroundCommand(child, c.Node, script, "--pairing-file", c.PairingFile, "--bridge-url", bridgeURL, "--cdp-port", strconv.Itoa(c.Port), "--parent-pid", strconv.Itoa(os.Getpid()))
							cmd.Stdout, cmd.Stderr = log, log
							cmd.WaitDelay = 3 * time.Second
							if e = cmd.Start(); e == nil {
								current = cmd
								cancel = closeChild
								done = make(chan error, 1)
								ch := done
								fingerprint = next
								go func() { ch <- cmd.Wait(); log.Close() }()
							} else {
								closeChild()
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
			cancel()
			current = nil
			cancel = nil
			done = nil
		case <-ticker.C:
		}
	}
}
func ConnectionError() error {
	return fmt.Errorf("Codex CDP control is not ready; keep LazyMind and the original Codex chat open")
}

// The supervisor may have to kill Node. Remove its native guest after the lock
// owner exits; never leave an interactive panel behind on disconnect/logout.
func CleanupPresentation(home string) error {
	c, err := readConfig(home)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready, err := DesktopReady(ctx, c.Port)
	if err != nil || !ready {
		return err
	}
	bundle, err := codexplugin.DispatcherBundle()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(bundle)
	script := filepath.Join(home, "codex-workflow", "connector-"+hex.EncodeToString(sum[:8])+".mjs")
	if err := os.WriteFile(script, bundle, 0600); err != nil {
		return err
	}
	command := backgroundCommand(ctx, c.Node, script, "--pairing-file", c.PairingFile, "--cdp-port", strconv.Itoa(c.Port), "--cleanup-only")
	return command.Run()
}
