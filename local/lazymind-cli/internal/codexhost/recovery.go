package codexhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/integrationstate"
)

type RecoveryRequest struct {
	ID string `json:"id"`
	ConnectionIssue
	ThreadID string           `json:"thread_id,omitempty"`
	ViewOnly bool             `json:"view_only,omitempty"`
	Intents  []RecoveryIntent `json:"intents,omitempty"`
}

// These records belong to LazyMind, not to the MCP process which exits with Codex.
type RecoveryIntent struct {
	ID          string          `json:"id"`
	ThreadID    string          `json:"thread_id"`
	Tool        string          `json:"tool"`
	Arguments   json.RawMessage `json:"arguments"`
	Fingerprint string          `json:"fingerprint"`
	Accepted    bool            `json:"accepted,omitempty"`
}

type RecoverySubmission struct {
	ConnectionIssue
	ThreadID string          `json:"thread_id,omitempty"`
	ViewOnly bool            `json:"view_only,omitempty"`
	Intent   *RecoveryIntent `json:"intent,omitempty"`
}

type recoveryIntentKey struct{}

func WithRecoveryIntent(ctx context.Context, resolve func() (*RecoveryIntent, error)) context.Context {
	return context.WithValue(ctx, recoveryIntentKey{}, resolve)
}

type RecoveryResponse struct {
	Request *RecoveryRequest `json:"request"`
}

// Observing a view cannot restart Codex, send a message, or settle a Workflow.
func PresentationReady(ctx context.Context, home, request string) (bool, error) {
	current, err := CurrentRecovery(ctx, home)
	if err != nil {
		return false, err
	}
	if current.Request == nil || current.Request.ID != request || !current.Request.ViewOnly {
		return false, nil
	}
	if err := CheckConnection(ctx, home); err != nil {
		return false, err
	}
	c, err := readConfig(home)
	if err != nil {
		return false, err
	}
	return runProbe(ctx, home, c, "--presentation-probe-only", "--thread-id", current.Request.ThreadID)
}

// Looking at a completed run still needs its existing Panel. This check never
// gates the read, saves an execution intent, or asks the Agent to advance a step.
func EnsurePresentation(ctx context.Context, home, thread string) (bool, error) {
	if _, err := threadLink(thread); err != nil {
		return false, err
	}
	err := CheckConnection(ctx, home)
	var issue *ConnectionIssue
	if err == nil || !errors.As(err, &issue) || issue.Code == "CODEX_INTEGRATION_DISABLED" {
		return false, err
	}
	var bound struct {
		Run string `json:"run_id"`
	}
	if err := recoveryCall(ctx, home, http.MethodGet, "/v1/codex/presentation?session_id="+url.QueryEscape(thread), nil, &bound); err != nil {
		return false, err
	}
	if bound.Run == "" {
		return false, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := recoveryCall(bounded, home, http.MethodPost, "/v1/codex/recovery", RecoverySubmission{ConnectionIssue: *issue, ThreadID: thread, ViewOnly: true}, nil); err != nil {
		return false, err
	}
	return true, nil
}

// Only execution admission requests a window. Health checks remain read-only.
// A brief retry lets an existing CDP connection recover without restarting Codex.
func EnsureConnection(ctx context.Context, home string) error {
	err := CheckConnection(ctx, home)
	var issue *ConnectionIssue
	if err == nil || !errors.As(err, &issue) || issue.Code == "CODEX_INTEGRATION_DISABLED" {
		return err
	}
	timer := time.NewTimer(350 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	err = CheckConnection(ctx, home)
	if err == nil || !errors.As(err, &issue) || issue.Code == "CODEX_INTEGRATION_DISABLED" {
		return err
	}
	// Failure to submit cannot authorize execution. Preserve the original issue.
	submission := RecoverySubmission{ConnectionIssue: *issue}
	if resolve, ok := ctx.Value(recoveryIntentKey{}).(func() (*RecoveryIntent, error)); ok {
		intent, resolveErr := resolve()
		if resolveErr != nil {
			return fmt.Errorf("cannot preserve the blocked Codex operation: %w", resolveErr)
		}
		submission.Intent, submission.ThreadID = intent, intent.ThreadID
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if notifyErr := recoveryCall(bounded, home, http.MethodPost, "/v1/codex/recovery", submission, nil); notifyErr != nil {
		return connectionIssue(issue.Code, "Codex session control is unavailable; LazyMind could not open its recovery window: "+notifyErr.Error(), issue.CanRestart)
	}
	return err
}

func AcceptRecoveryIntent(ctx context.Context, home, request, intent string) error {
	return recoveryCall(ctx, home, http.MethodPost, "/v1/codex/recovery/"+request+"/accepted/"+intent, nil, nil)
}

func CompleteRecovery(ctx context.Context, home, request string) error {
	return recoveryCall(ctx, home, http.MethodPost, "/v1/codex/recovery/"+request+"/complete", nil, nil)
}

func CompleteViewRecovery(ctx context.Context, home, request string) error {
	return recoveryCall(ctx, home, http.MethodPost, "/v1/codex/recovery/"+request+"/view-complete", nil, nil)
}

func WaitRecovery(ctx context.Context, home string) (RecoveryResponse, error) {
	var result RecoveryResponse
	err := recoveryCall(ctx, home, http.MethodGet, "/v1/codex/recovery", nil, &result)
	return result, err
}

func CurrentRecovery(ctx context.Context, home string) (RecoveryResponse, error) {
	var result RecoveryResponse
	err := recoveryCall(ctx, home, http.MethodGet, "/v1/codex/recovery?wait=0", nil, &result)
	return result, err
}

func DismissRecovery(ctx context.Context, home, id string) error {
	return recoveryCall(ctx, home, http.MethodDelete, "/v1/codex/recovery/"+id, nil, nil)
}

func recoveryCall(ctx context.Context, home, method, path string, input, output any) error {
	store, err := credentials.NewStore(home, "")
	if err != nil {
		return err
	}
	scope, err := store.AccountScope()
	if err != nil {
		return err
	}
	_, pair, err := Load(home, scope)
	if err != nil {
		return err
	}
	address, err := connectorconfig.BridgeAddress()
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		encoded, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+pair.Token)
	req.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("recovery redirect refused") }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Codex recovery request returned HTTP %d", response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
	}
	return nil
}

type recoveryProgressKey struct{}

func reportRecovery(ctx context.Context, stage string) {
	if report, ok := ctx.Value(recoveryProgressKey{}).(func(string)); ok {
		report(stage)
	}
}

// Reconnect runs in a LazyMind-owned CLI child, never in Codex's MCP process.
// It reuses the saved installation and pairing; it does not install or migrate.
func Reconnect(ctx context.Context, home string, progress func(string)) error {
	return reconnect(ctx, home, progress, nil)
}

func reconnect(ctx context.Context, home string, progress func(string), restore func(context.Context) error) error {
	store, err := credentials.NewStore(home, "")
	if err != nil {
		return err
	}
	scope, err := store.AccountScope()
	if err != nil {
		return err
	}
	_, pair, err := Load(home, scope)
	if err != nil {
		return err
	}
	guarded, cancel := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	defer func() { cancel(); <-watchDone }()
	if progress != nil {
		guarded = context.WithValue(guarded, recoveryProgressKey{}, progress)
	}
	// Logging out, changing account, or disabling the pairing revokes recovery.
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-guarded.Done():
				return
			case <-ticker.C:
				current, e := store.AccountScope()
				_, latest, loadErr := Load(home, current)
				if e != nil || current != scope || loadErr != nil || latest.Token != pair.Token || !integrationstate.Allowed(home, "codex") {
					cancel()
					return
				}
			}
		}
	}()
	if err := PrepareDesktop(guarded, home); err != nil {
		return err
	}
	if err := guarded.Err(); err != nil {
		return err
	}
	if restore != nil {
		if err := restore(guarded); err != nil {
			return err
		}
	}
	reportRecovery(guarded, "connected")
	return nil
}
