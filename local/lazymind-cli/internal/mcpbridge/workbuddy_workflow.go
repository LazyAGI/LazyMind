package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowhost"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

func (b *Bridge) workbuddyPair() (workflowhost.Pairing, error) {
	store, err := credentials.NewStore(b.home, "")
	if err != nil {
		return workflowhost.Pairing{}, err
	}
	account, err := store.AccountScope()
	if err != nil {
		return workflowhost.Pairing{}, err
	}
	_, pair, err := workbuddyhost.Load(b.home, account)
	return pair, err
}
func (b *Bridge) resolveWorkBuddyDriver(ctx context.Context, args json.RawMessage) (string, error) {
	pair, err := b.workbuddyPair()
	if err != nil {
		return "", err
	}
	store, err := credentials.NewStore(b.home, "")
	if err != nil {
		return "", err
	}
	account, err := store.AccountScope()
	if err != nil {
		return "", err
	}
	config, _, err := workbuddyhost.Load(b.home, account)
	if err != nil {
		return "", err
	}
	status, err := workbuddyhost.ReadStatus(config, pair.ConnectorID)
	if err != nil || !status.Ready {
		return "", errors.New("WorkBuddy CDP connector is offline or incompatible")
	}
	body, _ := json.Marshal(map[string]any{"arguments": args})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, status.BridgeURL+"/v1/workbuddy/resolve", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+pair.Token)
	req.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("identity bridge redirects are not allowed")
	}}
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("WorkBuddy connector unavailable; start Assistant Bridge and enable CDP")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("WorkBuddy native workflow.start identity unavailable; use a stable idempotency_key and retry")
	}
	var identity workbuddyhost.Identity
	if err := json.NewDecoder(response.Body).Decode(&identity); err != nil || identity.SessionID == "" || identity.RequestID == "" || identity.ToolCallID == "" {
		return "", errors.New("incomplete WorkBuddy native identity")
	}
	slog.Info("workbuddy identity resolved", "connector_id", pair.ConnectorID, "native_session_id", identity.SessionID,
		"native_request_id", identity.RequestID, "tool_call_id", identity.ToolCallID)
	return identity.SessionID, nil
}
func (b *Bridge) bindWorkBuddyController(ctx context.Context, runID, sessionID string) error {
	pair, err := b.workbuddyPair()
	if err != nil {
		return err
	}
	var result map[string]any
	started := time.Now()
	err = b.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(runID)+"/host-binding", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "provider": "workbuddy",
		"driver_session_id": sessionID, "cancellation": "session",
	}, &result)
	status := "bound"
	if err != nil {
		status = "unconfirmed"
	}
	slog.Info("workbuddy binding", "connector_id", pair.ConnectorID, "run_id", runID, "native_session_id", sessionID,
		"status", status, "duration_ms", time.Since(started).Milliseconds())
	return err
}
