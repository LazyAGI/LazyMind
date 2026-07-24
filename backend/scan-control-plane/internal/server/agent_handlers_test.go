package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	scheduleengine "github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestAgentReportEventsEnqueuesWatchSync(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 13, 10, 20, 0, 0, time.UTC)
	occurredAt := now.Add(-time.Minute)
	agents := &agentStoreStub{
		bindings: []store.Binding{{
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			AgentID:           "agent-1",
			SyncMode:          "watch",
			Status:            "ACTIVE",
		}},
	}
	scheduler := &watchSchedulerStub{runID: "sync-run-1"}
	handler := NewHandler(
		WithAgentStore(agents),
		WithScheduleEngine(scheduler),
		WithAgentToken("agent-token"),
		WithClock(func() time.Time { return now }),
	)

	req := agentRequest(t, "/api/v1/agents/events", map[string]any{
		"agent_id": "agent-1",
		"events": []map[string]any{{
			"source_id":   "source-1",
			"tenant_id":   "tenant-1",
			"event_type":  "modified",
			"path":        "/workspace/docs/a.md",
			"object_key":  "local_fs:agent-1:path:/workspace/docs/a.md",
			"is_dir":      false,
			"occurred_at": occurredAt.Format(time.RFC3339Nano),
		}},
	})
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d body=%s", w.Code, w.Body.String())
	}
	if agents.lastSourceID != "source-1" || agents.lastBindingAgentID != "agent-1" {
		t.Fatalf("event did not query watch binding by source and agent: %+v", agents)
	}
	if len(scheduler.requests) != 1 {
		t.Fatalf("expected one watch event schedule request, got %+v", scheduler.requests)
	}
	scheduled := scheduler.requests[0]
	if scheduled.Binding.BindingID != "binding-1" || scheduled.EventType != "modified" || scheduled.Path != "/workspace/docs/a.md" {
		t.Fatalf("event fields were not preserved: %+v", scheduled)
	}
	if !scheduled.OccurredAt.Equal(occurredAt) || scheduled.ObjectKey != "local_fs:agent-1:path:/workspace/docs/a.md" {
		t.Fatalf("event identity was not preserved: %+v", scheduled)
	}
	var resp agentReportEventsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Accepted || len(resp.JobIDs) != 1 || resp.JobIDs[0] != "sync-run-1" || len(resp.Errors) != 0 {
		t.Fatalf("unexpected report response: %+v", resp)
	}
}

func TestAgentEndpointsRequireBearerToken(t *testing.T) {
	t.Parallel()

	handler := NewHandler(
		WithAgentStore(&agentStoreStub{}),
		WithScheduleEngine(&watchSchedulerStub{}),
		WithAgentToken("agent-token"),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader([]byte(`{"agent_id":"agent-1","tenant_id":"tenant-1"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentRegisterAndHeartbeatUpsertAgent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 13, 10, 30, 0, 0, time.UTC)
	agents := &agentStoreStub{}
	handler := NewHandler(
		WithAgentStore(agents),
		WithAgentToken("agent-token"),
		WithClock(func() time.Time { return now }),
	)

	register := agentRequest(t, "/api/v1/agents/register", map[string]any{
		"agent_id":    "agent-1",
		"tenant_id":   "tenant-1",
		"hostname":    "host-a",
		"version":     "v1",
		"listen_addr": "127.0.0.1:19090",
	})
	registerResp := httptest.NewRecorder()
	handler.ServeHTTP(registerResp, register)
	if registerResp.Code != http.StatusOK {
		t.Fatalf("register expected OK, got %d body=%s", registerResp.Code, registerResp.Body.String())
	}
	if agents.upserts[0].Status != "ONLINE" || !agents.upserts[0].LastHeartbeatAt.Equal(now) {
		t.Fatalf("register did not upsert online agent: %+v", agents.upserts[0])
	}

	heartbeatAt := now.Add(-10 * time.Second)
	heartbeat := agentRequest(t, "/api/v1/agents/heartbeat", map[string]any{
		"agent_id":           "agent-1",
		"tenant_id":          "tenant-1",
		"hostname":           "host-a",
		"version":            "v1",
		"status":             "DEGRADED",
		"last_heartbeat_at":  heartbeatAt.Format(time.RFC3339Nano),
		"source_count":       2,
		"active_watch_count": 1,
		"active_task_count":  3,
	})
	heartbeatResp := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatResp, heartbeat)
	if heartbeatResp.Code != http.StatusOK {
		t.Fatalf("heartbeat expected OK, got %d body=%s", heartbeatResp.Code, heartbeatResp.Body.String())
	}
	got := agents.upserts[1]
	if got.Status != "DEGRADED" || got.ActiveSourceCount != 2 || got.ActiveWatchCount != 1 || got.ActiveTaskCount != 3 || !got.LastHeartbeatAt.Equal(heartbeatAt) {
		t.Fatalf("heartbeat did not preserve agent state: %+v", got)
	}
}

func TestAgentRegisterAndHeartbeatAllowEmptyTenant(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 13, 10, 35, 0, 0, time.UTC)
	agents := &agentStoreStub{}
	handler := NewHandler(
		WithAgentStore(agents),
		WithAgentToken("agent-token"),
		WithClock(func() time.Time { return now }),
	)

	register := agentRequest(t, "/api/v1/agents/register", map[string]any{
		"agent_id": "agent-1",
	})
	registerResp := httptest.NewRecorder()
	handler.ServeHTTP(registerResp, register)
	if registerResp.Code != http.StatusOK {
		t.Fatalf("register expected OK, got %d body=%s", registerResp.Code, registerResp.Body.String())
	}
	if got := agents.upserts[0]; got.TenantID != "" || got.Status != "ONLINE" {
		t.Fatalf("register did not preserve empty-tenant online agent: %+v", got)
	}

	heartbeat := agentRequest(t, "/api/v1/agents/heartbeat", map[string]any{
		"agent_id": "agent-1",
		"status":   "ONLINE",
	})
	heartbeatResp := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatResp, heartbeat)
	if heartbeatResp.Code != http.StatusOK {
		t.Fatalf("heartbeat expected OK, got %d body=%s", heartbeatResp.Code, heartbeatResp.Body.String())
	}
	if got := agents.upserts[1]; got.TenantID != "" || got.Status != "ONLINE" {
		t.Fatalf("heartbeat did not preserve empty-tenant online agent: %+v", got)
	}
}

func TestAgentRegisterQueuesLocalWatcherStartCommands(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 13, 10, 37, 0, 0, time.UTC)
	agents := &agentStoreStub{
		localBindings: []store.Binding{{
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 2,
			AgentID:           "agent-1",
			ConnectorType:     "local_fs",
			TargetType:        "local_path",
			TargetRef:         "/workspace/docs",
			SyncMode:          "manual",
			Status:            "ACTIVE",
		}},
	}
	handler := NewHandler(
		WithAgentStore(agents),
		WithAgentToken("agent-token"),
		WithClock(func() time.Time { return now }),
	)

	register := agentRequest(t, "/api/v1/agents/register", map[string]any{
		"agent_id":  "agent-1",
		"tenant_id": "tenant-1",
		"hostname":  "host-a",
		"version":   "v1",
	})
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, register)
	if resp.Code != http.StatusOK {
		t.Fatalf("register expected OK, got %d body=%s", resp.Code, resp.Body.String())
	}
	if agents.lastLocalWatcherAgentID != "agent-1" {
		t.Fatalf("register did not reconcile local watcher bindings: %+v", agents)
	}
	if len(agents.createdCommands) != 1 {
		t.Fatalf("expected one start_source command, got %+v", agents.createdCommands)
	}
	command := agents.createdCommands[0]
	if _, err := strconv.ParseInt(command.CommandID, 10, 64); err != nil {
		t.Fatalf("agent command id must be numeric for ack, got %q", command.CommandID)
	}
	if command.AgentID != "agent-1" || command.CommandType != "start_source" || command.Status != "PENDING" {
		t.Fatalf("unexpected command identity: %+v", command)
	}
	if command.Payload["type"] != "start_source" || command.Payload[agentCommandRootKey()] != "/workspace/docs" || command.Payload["skip_initial_scan"] != true {
		t.Fatalf("start_source payload does not match file-watcher contract: %+v", command.Payload)
	}
	if command.Payload["tenant_id"] != "tenant-1" || command.Payload["source_id"] != "source-1" {
		t.Fatalf("start_source payload lost source identity: %+v", command.Payload)
	}
}

func TestAgentPullAndAckCommands(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 13, 10, 40, 0, 0, time.UTC)
	agents := &agentStoreStub{
		commands: []store.AgentCommand{{
			CommandID:   "42",
			AgentID:     "agent-1",
			CommandType: "start_source",
			Payload: store.JSON{
				"type":                "start_source",
				"tenant_id":           "tenant-1",
				"source_id":           "source-1",
				agentCommandRootKey(): "/workspace/docs",
				"skip_initial_scan":   true,
			},
		}},
	}
	handler := NewHandler(
		WithAgentStore(agents),
		WithAgentToken("agent-token"),
		WithClock(func() time.Time { return now }),
	)

	pull := agentRequest(t, "/api/v1/agents/pull", map[string]any{"agent_id": "agent-1", "tenant_id": "tenant-1"})
	pullResp := httptest.NewRecorder()
	handler.ServeHTTP(pullResp, pull)
	if pullResp.Code != http.StatusOK {
		t.Fatalf("pull expected OK, got %d body=%s", pullResp.Code, pullResp.Body.String())
	}
	var decoded map[string]any
	if err := json.NewDecoder(pullResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode pull response: %v", err)
	}
	commands := decoded["commands"].([]any)
	command := commands[0].(map[string]any)
	if command[agentCommandRootKey()] != "/workspace/docs" || command["type"] != "start_source" || command["skip_initial_scan"] != true {
		t.Fatalf("pull response did not preserve file-watcher command contract: %#v", command)
	}

	ack := agentRequest(t, "/api/v1/agents/commands/ack", map[string]any{
		"agent_id":    "agent-1",
		"command_id":  42,
		"success":     true,
		"result_json": `{"started":true}`,
	})
	ackResp := httptest.NewRecorder()
	handler.ServeHTTP(ackResp, ack)
	if ackResp.Code != http.StatusOK {
		t.Fatalf("ack expected OK, got %d body=%s", ackResp.Code, ackResp.Body.String())
	}
	if agents.lastAck.CommandID != "42" || !agents.lastAck.Success || agents.lastAck.Result["started"] != true || !agents.lastAck.AckedAt.Equal(now) {
		t.Fatalf("ack did not preserve result: %+v", agents.lastAck)
	}
}

func agentRequest(t *testing.T, path string, body any) *http.Request {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer agent-token")
	return req
}

type agentStoreStub struct {
	upserts                 []store.Agent
	bindings                []store.Binding
	localBindings           []store.Binding
	commands                []store.AgentCommand
	createdCommands         []store.AgentCommand
	lastSourceID            string
	lastBindingAgentID      string
	lastLocalWatcherAgentID string
	lastPullAgentID         string
	lastPullLimit           int
	lastPullNow             time.Time
	lastAck                 store.AgentCommandAck
}

func (s *agentStoreStub) UpsertAgent(_ context.Context, agent store.Agent) error {
	s.upserts = append(s.upserts, agent)
	return nil
}

func (s *agentStoreStub) ListWatchBindingsForAgentEvent(_ context.Context, sourceID, agentID string) ([]store.Binding, error) {
	s.lastSourceID = sourceID
	s.lastBindingAgentID = agentID
	return s.bindings, nil
}

func (s *agentStoreStub) ListLocalWatcherBindingsForAgent(_ context.Context, agentID string) ([]store.Binding, error) {
	s.lastLocalWatcherAgentID = agentID
	return s.localBindings, nil
}

func (s *agentStoreStub) CreateAgentCommand(_ context.Context, command store.AgentCommand) error {
	s.createdCommands = append(s.createdCommands, command)
	return nil
}

func (s *agentStoreStub) ListPendingAgentCommands(_ context.Context, agentID string, now time.Time, limit int) ([]store.AgentCommand, error) {
	s.lastPullAgentID = agentID
	s.lastPullNow = now
	s.lastPullLimit = limit
	return s.commands, nil
}

func (s *agentStoreStub) AckAgentCommand(_ context.Context, ack store.AgentCommandAck) error {
	s.lastAck = ack
	return nil
}

type watchSchedulerStub struct {
	runID    string
	requests []scheduleengine.WatchEventSyncRequest
	err      error
}

func (s *watchSchedulerStub) EnqueueWatchEventSync(_ context.Context, req scheduleengine.WatchEventSyncRequest) (scheduleengine.SyncRunIntent, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return scheduleengine.SyncRunIntent{}, s.err
	}
	runID := s.runID
	if runID == "" {
		runID = "sync-run-1"
	}
	return scheduleengine.SyncRunIntent{
		Run: store.SyncRun{
			RunID:       runID,
			SourceID:    req.Binding.SourceID,
			BindingID:   req.Binding.BindingID,
			TriggerType: scheduleengine.TriggerTypeWatch,
			ScopeType:   string(connector.ScopeTypeWatchEvent),
		},
		Created: true,
	}, nil
}
