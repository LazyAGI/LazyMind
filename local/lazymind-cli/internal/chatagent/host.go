package chatagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lazymind/agentconnector/internal/agentexec"
)

const (
	heartbeatInterval       = 2 * time.Second
	claimRequestTimeout     = 15 * time.Second
	heartbeatRequestTimeout = 5 * time.Second
	eventRequestTimeout     = 5 * time.Second
	leaseLossGrace          = 20 * time.Second
	eventRetryDelay         = 200 * time.Millisecond
	terminalTimeout         = 10 * time.Second
	sessionCatalogInterval  = time.Minute
)

type Run struct {
	RunID            string `json:"run_id"`
	ConversationID   string `json:"conversation_id"`
	HistoryID        string `json:"history_id"`
	Provider         string `json:"provider"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	Action           string `json:"action"`
	Prompt           string `json:"prompt"`
	Query            string `json:"query"`
	LeaseToken       string `json:"lease_token"`
	HostID           string `json:"host_id"`
}

type Event struct {
	Type             string `json:"type"`
	Text             string `json:"text,omitempty"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	Error            string `json:"error,omitempty"`
}

type Runner interface {
	Run(context.Context, Run, func(Event) error) error
}

type availabilityReporter interface {
	Availability() (bool, string)
}

type NativeSession struct {
	ThreadID      string       `json:"thread_id"`
	ProjectKey    string       `json:"project_key"`
	ProjectName   string       `json:"project_name"`
	DisplayName   string       `json:"display_name"`
	NativeUpdated time.Time    `json:"native_updated_at"`
	TurnCount     int          `json:"turn_count"`
	Turns         []NativeTurn `json:"turns,omitempty"`
}

type NativeTurn struct {
	ID        string    `json:"turn_id"`
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
	CreatedAt time.Time `json:"created_at"`
	Managed   bool      `json:"managed,omitempty"`
}

type syncSessionCatalogResponse struct {
	Updated  int `json:"updated"`
	Rejected int `json:"rejected"`
}

type SessionCatalog interface {
	Sessions(context.Context) ([]NativeSession, error)
}

type coreClient interface {
	DoJSON(context.Context, string, string, any, any) error
}

type Host struct {
	api               coreClient
	runner            Runner
	provider          string
	id                string
	installed         bool
	ready             bool
	unavailableReason string
	catalogMu         sync.Mutex
}

func NewHost(api coreClient, runner Runner, provider string) (*Host, error) {
	if api == nil || runner == nil {
		return nil, errors.New("Core client and Agent runner are required")
	}
	id, provider, err := newHostIdentity(provider)
	if err != nil {
		return nil, err
	}
	return &Host{
		api: api, runner: runner, provider: provider, id: id,
		installed: true, ready: true,
	}, nil
}

func newHostIdentity(provider string) (string, string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "", "", errors.New("Agent provider is required")
	}
	id, err := agentexec.PersistentHostID()
	return id, provider, err
}

// NewUnavailableHost reports process discovery failures to Core without ever
// claiming a Chat run. This keeps installation knowledge in the local ACL.
func NewUnavailableHost(api coreClient, provider string, reason error) (*Host, error) {
	if api == nil || reason == nil {
		return nil, errors.New("Core client and unavailability reason are required")
	}
	id, provider, err := newHostIdentity(provider)
	if err != nil {
		return nil, err
	}
	return &Host{
		api: api, provider: provider, id: id, unavailableReason: reason.Error(),
	}, nil
}

func (h *Host) Run(ctx context.Context) error {
	catalogCtx, cancelCatalog := context.WithCancel(ctx)
	catalogDone := make(chan struct{})
	go func() {
		defer close(catalogDone)
		h.runSessionCatalog(catalogCtx)
	}()
	defer func() {
		cancelCatalog()
		<-catalogDone
	}()
	for ctx.Err() == nil {
		if status, ok := h.runner.(availabilityReporter); ok {
			h.ready, h.unavailableReason = status.Availability()
		}
		var response struct {
			Run *Run `json:"run"`
		}
		path := "/external-chat/hosts/" + url.PathEscape(h.provider) + "/claim"
		if err := h.doJSON(ctx, claimRequestTimeout, http.MethodPost, path, map[string]any{
			"host_id": h.id, "installed": h.installed, "ready": h.ready,
			"unavailable_reason": h.unavailableReason,
		}, &response); err != nil {
			if !waitRetry(ctx) {
				return ctx.Err()
			}
			continue
		}
		if response.Run == nil {
			continue
		}
		if response.Run.HostID != h.id || response.Run.Provider != h.provider {
			// Core is authoritative for both values. Never execute a claimed
			// capability addressed to another Host or provider adapter.
			continue
		}
		if h.runner == nil {
			continue
		}
		h.execute(ctx, *response.Run)
		h.syncSessionCatalog(ctx)
	}
	return ctx.Err()
}

func (h *Host) runSessionCatalog(ctx context.Context) {
	if _, ok := h.runner.(SessionCatalog); !ok {
		return
	}
	h.runFullSessionCatalog(ctx)
}

func (h *Host) runFullSessionCatalog(ctx context.Context) {
	delay := time.Duration(0)
	for ctx.Err() == nil {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if h.syncSessionCatalog(ctx) {
			delay = sessionCatalogInterval
		} else {
			delay = 10 * time.Second
		}
	}
}

func (h *Host) syncSessionCatalog(ctx context.Context) bool {
	h.catalogMu.Lock()
	defer h.catalogMu.Unlock()
	catalog, ok := h.runner.(SessionCatalog)
	if !ok {
		return true
	}
	sessions, err := catalog.Sessions(ctx)
	if err != nil {
		return false
	}
	return h.syncSessionBatches(ctx, sessions, true)
}

func (h *Host) syncSessionBatches(ctx context.Context, sessions []NativeSession, reset bool) bool {
	batches := sessionCatalogBatches(sessions)
	for index, batch := range batches {
		requestCtx, cancel := context.WithTimeout(ctx, claimRequestTimeout)
		var response syncSessionCatalogResponse
		err := h.api.DoJSON(requestCtx, http.MethodPost, "/external-chat/providers/"+url.PathEscape(h.provider)+"/sessions:sync", map[string]any{
			"host_id": h.id, "sessions": batch, "reset": reset && index == 0,
		}, &response)
		cancel()
		if err != nil || response.Updated != len(batch) || response.Rejected != 0 {
			return false
		}
	}
	return true
}

func sessionCatalogBatches(sessions []NativeSession) [][]NativeSession {
	const maxBatchBytes = 8 << 20
	if len(sessions) == 0 {
		return [][]NativeSession{{}}
	}
	expanded := make([]NativeSession, 0, len(sessions))
	for _, session := range sessions {
		if session.TurnCount == 0 {
			session.TurnCount = len(session.Turns)
		}
		encoded, _ := json.Marshal(session)
		if len(encoded) <= maxBatchBytes || len(session.Turns) < 2 {
			expanded = append(expanded, session)
			continue
		}
		base := session
		base.Turns = nil
		chunk := base
		for _, turn := range session.Turns {
			candidate := chunk
			candidate.Turns = append(append([]NativeTurn(nil), chunk.Turns...), turn)
			body, _ := json.Marshal(candidate)
			if len(chunk.Turns) > 0 && len(body) > maxBatchBytes {
				expanded = append(expanded, chunk)
				chunk = base
			}
			chunk.Turns = append(chunk.Turns, turn)
		}
		if len(chunk.Turns) > 0 {
			expanded = append(expanded, chunk)
		}
	}
	batches := make([][]NativeSession, 0, len(expanded)/100+1)
	batch := make([]NativeSession, 0, 100)
	batchBytes := 0
	for _, session := range expanded {
		encoded, _ := json.Marshal(session)
		if len(batch) > 0 && (len(batch) >= 250 || batchBytes+len(encoded) > maxBatchBytes) {
			batches = append(batches, batch)
			batch = make([]NativeSession, 0, 100)
			batchBytes = 0
		}
		batch = append(batch, session)
		batchBytes += len(encoded)
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

func (h *Host) execute(parent context.Context, run Run) {
	if run.Action == "finalize" {
		_ = h.sendTerminalEvent(parent, run, Event{Type: "completed"})
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		lastLeaseConfirmation := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var response struct {
					StopRequested bool `json:"stop_requested"`
				}
				path := "/external-chat/runs/" + url.PathEscape(run.RunID) + ":heartbeat"
				if err := h.doJSON(ctx, heartbeatRequestTimeout, http.MethodPost, path, map[string]any{
					"host_id": h.id, "lease_token": run.LeaseToken,
				}, &response); err != nil {
					// Core may be restarting. Continue only inside a window that is
					// strictly shorter than Core's 30-second lease, so another Host
					// can never claim while this process is still executing.
					if time.Since(lastLeaseConfirmation) >= leaseLossGrace {
						cancel()
						return
					}
					continue
				}
				lastLeaseConfirmation = time.Now()
				if response.StopRequested {
					cancel()
					return
				}
			}
		}
	}()
	emit := func(event Event) error {
		return h.sendEvent(ctx, run, event)
	}
	err := h.runner.Run(ctx, run, emit)
	if parent.Err() != nil || errors.Is(err, context.Canceled) {
		cancel()
		<-done
		return
	}
	terminal := Event{Type: "completed"}
	if err != nil {
		terminal = Event{Type: "failed", Error: err.Error()}
	}
	// Keep the heartbeat active until Core durably accepts the terminal event.
	// A transient response loss must not leave a completed provider turn stuck
	// in running state or expose its lease to another Host.
	_ = h.sendTerminalEvent(parent, run, terminal)
	cancel()
	<-done
}

func (h *Host) sendEvent(ctx context.Context, run Run, event Event) error {
	return h.sendEventWithRetry(ctx, run, event, 0)
}

func (h *Host) doJSON(ctx context.Context, timeout time.Duration, method, path string, input, output any) error {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return h.api.DoJSON(requestCtx, method, path, input, output)
}

func (h *Host) sendEventWithRetry(ctx context.Context, run Run, event Event, maxAttempts int) error {
	eventID, err := newEventID()
	if err != nil {
		return err
	}
	path := "/external-chat/runs/" + url.PathEscape(run.RunID) + ":event"
	input := map[string]any{
		"host_id": h.id, "lease_token": run.LeaseToken,
		"event_id": eventID, "type": event.Type,
	}
	if event.Text != "" {
		input["text"] = event.Text
	}
	if event.ProviderThreadID != "" {
		input["provider_thread_id"] = event.ProviderThreadID
	}
	if event.Error != "" {
		input["error"] = event.Error
	}
	for attempt := 0; maxAttempts <= 0 || attempt < maxAttempts; attempt++ {
		if err = h.doJSON(ctx, eventRequestTimeout, http.MethodPost, path, input, nil); err == nil {
			return nil
		}
		if maxAttempts > 0 && attempt+1 >= maxAttempts {
			break
		}
		if !waitEventRetry(ctx) {
			break
		}
	}
	return err
}

func (h *Host) sendTerminalEvent(parent context.Context, run Run, event Event) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), terminalTimeout)
	defer cancel()
	return h.sendEventWithRetry(ctx, run, event, 0)
}

func newEventID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "evt-" + hex.EncodeToString(value), nil
}

func waitEventRetry(ctx context.Context) bool {
	timer := time.NewTimer(eventRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func waitRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (h *Host) String() string { return fmt.Sprintf("%s/%s", h.provider, h.id) }
