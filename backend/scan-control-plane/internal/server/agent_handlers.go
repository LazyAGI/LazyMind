package server

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/access"
	scheduleengine "github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type agentRegisterRequest struct {
	AgentID    string `json:"agent_id"`
	TenantID   string `json:"tenant_id"`
	Hostname   string `json:"hostname"`
	Version    string `json:"version"`
	ListenAddr string `json:"listen_addr,omitempty"`
}

type agentHeartbeatRequest struct {
	AgentID          string         `json:"agent_id"`
	TenantID         string         `json:"tenant_id"`
	Hostname         string         `json:"hostname"`
	Version          string         `json:"version"`
	Status           string         `json:"status"`
	LastHeartbeatAt  time.Time      `json:"last_heartbeat_at"`
	SourceCount      int64          `json:"source_count"`
	ActiveWatchCount int64          `json:"active_watch_count"`
	ActiveTaskCount  int64          `json:"active_task_count"`
	ListenAddr       string         `json:"listen_addr,omitempty"`
	LastError        string         `json:"last_error,omitempty"`
	ResourceUsage    map[string]any `json:"resource_usage_json,omitempty"`
}

type agentFileEvent struct {
	SourceID   string    `json:"source_id"`
	BindingID  string    `json:"binding_id,omitempty"`
	TenantID   string    `json:"tenant_id"`
	EventType  string    `json:"event_type"`
	Path       string    `json:"path"`
	ObjectKey  string    `json:"object_key,omitempty"`
	OldPath    string    `json:"old_path,omitempty"`
	IsDir      bool      `json:"is_dir"`
	OccurredAt time.Time `json:"occurred_at"`
	TraceID    string    `json:"trace_id,omitempty"`
}

type agentReportEventsRequest struct {
	AgentID string           `json:"agent_id"`
	Events  []agentFileEvent `json:"events"`
}

type agentPullCommandsRequest struct {
	AgentID  string `json:"agent_id"`
	TenantID string `json:"tenant_id"`
}

type agentCommandResponse struct {
	ID              int64  `json:"id"`
	Type            string `json:"type"`
	TenantID        string `json:"tenant_id,omitempty"`
	SourceID        string `json:"source_id,omitempty"`
	BindingID       string `json:"binding_id,omitempty"`
	RootPath        string `json:"-"`
	Mode            string `json:"mode,omitempty"`
	Reason          string `json:"reason,omitempty"`
	SkipInitialScan bool   `json:"skip_initial_scan,omitempty"`
	DocumentID      string `json:"document_id,omitempty"`
	VersionID       string `json:"version_id,omitempty"`
	SrcPath         string `json:"src_path,omitempty"`
}

type agentPullCommandsResponse struct {
	Commands []agentCommandResponse `json:"commands"`
}

type agentAckCommandRequest struct {
	AgentID   string `json:"agent_id"`
	CommandID int64  `json:"command_id"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	Result    string `json:"result_json,omitempty"`
}

type agentAcceptedResponse struct {
	Accepted bool `json:"accepted"`
}

type agentReportEventsResponse struct {
	Accepted bool       `json:"accepted"`
	JobIDs   []string   `json:"job_ids,omitempty"`
	Errors   []JobError `json:"errors,omitempty"`
}

type JobError = sourceengine.JobError

const agentCommandRootKeyPrefix = "root"
const agentCommandRootKeySuffix = "_path"

func (h *Handler) agentRegister(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAgent(w, r) {
		return
	}
	if h.agents == nil {
		writeError(w, missingDependency("agent store"))
		return
	}
	var req agentRegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	agent := agentFromRegister(req, h.clock().UTC())
	if err := h.upsertAgent(r, agent); err != nil {
		writeError(w, err)
		return
	}
	if err := h.queueLocalWatcherStartsForAgent(r, agent.AgentID, agent.TenantID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentAcceptedResponse{Accepted: true})
}

func (h *Handler) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAgent(w, r) {
		return
	}
	if h.agents == nil {
		writeError(w, missingDependency("agent store"))
		return
	}
	var req agentHeartbeatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	agent := agentFromHeartbeat(req, h.clock().UTC())
	if err := h.upsertAgent(r, agent); err != nil {
		writeError(w, err)
		return
	}
	if req.SourceCount == 0 && req.ActiveWatchCount == 0 {
		if err := h.queueLocalWatcherStartsForAgent(r, agent.AgentID, agent.TenantID); err != nil {
			writeError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, agentAcceptedResponse{Accepted: true})
}

func (h *Handler) queueLocalWatcherStartsForAgent(r *http.Request, agentID, tenantID string) error {
	bindings, err := h.agents.ListLocalWatcherBindingsForAgent(r.Context(), agentID)
	if err != nil {
		return err
	}
	now := h.clock().UTC()
	for index, binding := range bindings {
		command := store.AgentCommand{
			CommandID:   agentCommandID(agentID, binding, now, index),
			AgentID:     agentID,
			CommandType: "start_source",
			Status:      "PENDING",
			LastError:   store.JSON{},
			Result:      store.JSON{},
			CreatedAt:   now.Add(time.Duration(index) * time.Nanosecond),
			Payload: store.JSON{
				"type":                "start_source",
				"tenant_id":           tenantID,
				"source_id":           binding.SourceID,
				"binding_id":          binding.BindingID,
				agentCommandRootKey(): binding.TargetRef,
				"skip_initial_scan":   true,
			},
		}
		if err := h.agents.CreateAgentCommand(r.Context(), command); err != nil {
			return err
		}
	}
	return nil
}

func agentCommandID(agentID string, binding store.Binding, now time.Time, index int) string {
	seed := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d", agentID, binding.SourceID, binding.BindingID, binding.BindingGeneration, now.UnixNano(), index)
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(seed))
	value := hash.Sum64() & ((uint64(1) << 63) - 1)
	if value == 0 {
		value = 1
	}
	return strconv.FormatUint(value, 10)
}

func (h *Handler) agentReportEvents(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAgent(w, r) {
		return
	}
	if h.agents == nil {
		writeError(w, missingDependency("agent store"))
		return
	}
	if h.scheduler == nil {
		writeError(w, missingDependency("schedule engine"))
		return
	}
	var req agentReportEventsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID == "" {
		writeError(w, sourceengine.FieldError("agent_id", "required"))
		return
	}
	resp := agentReportEventsResponse{Accepted: true}
	for _, event := range req.Events {
		jobIDs, errors := h.enqueueAgentEvent(r, req.AgentID, event)
		resp.JobIDs = append(resp.JobIDs, jobIDs...)
		resp.Errors = append(resp.Errors, errors...)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) agentPullCommands(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAgent(w, r) {
		return
	}
	if h.agents == nil {
		writeError(w, missingDependency("agent store"))
		return
	}
	var req agentPullCommandsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	commands, err := h.agents.ListPendingAgentCommands(r.Context(), strings.TrimSpace(req.AgentID), h.clock().UTC(), 50)
	if err != nil {
		writeError(w, err)
		return
	}
	resp := agentPullCommandsResponse{Commands: make([]agentCommandResponse, 0, len(commands))}
	for _, command := range commands {
		resp.Commands = append(resp.Commands, agentCommandToResponse(command))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) agentAckCommand(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAgent(w, r) {
		return
	}
	if h.agents == nil {
		writeError(w, missingDependency("agent store"))
		return
	}
	var req agentAckCommandRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	result, err := decodeAgentResult(req.Result)
	if err != nil {
		writeError(w, sourceengine.FieldError("result_json", err.Error()))
		return
	}
	if err := h.agents.AckAgentCommand(r.Context(), store.AgentCommandAck{
		AgentID:   strings.TrimSpace(req.AgentID),
		CommandID: strconv.FormatInt(req.CommandID, 10),
		Success:   req.Success,
		Error:     req.Error,
		Result:    result,
		AckedAt:   h.clock().UTC(),
	}); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentAcceptedResponse{Accepted: true})
}

func (h *Handler) authorizeAgent(w http.ResponseWriter, r *http.Request) bool {
	if h.agentToken == "" {
		writeError(w, missingDependency("agent token"))
		return false
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) != h.agentToken {
		writeError(w, access.NewError(access.ErrCodeUnauthorized, "invalid agent token"))
		return false
	}
	return true
}

func (h *Handler) upsertAgent(r *http.Request, agent store.Agent) error {
	if strings.TrimSpace(agent.AgentID) == "" {
		return sourceengine.FieldError("agent_id", "required")
	}
	if agent.LastHeartbeatAt.IsZero() {
		agent.LastHeartbeatAt = h.clock().UTC()
	}
	if agent.UpdatedAt.IsZero() {
		agent.UpdatedAt = h.clock().UTC()
	}
	return h.agents.UpsertAgent(r.Context(), agent)
}

func (h *Handler) enqueueAgentEvent(r *http.Request, agentID string, event agentFileEvent) ([]string, []JobError) {
	if strings.TrimSpace(event.SourceID) == "" {
		return nil, []JobError{{Code: string(sourceengine.ErrCodeInvalidRequest), Message: "source_id is required"}}
	}
	bindings, err := h.agents.ListWatchBindingsForAgentEvent(r.Context(), event.SourceID, agentID)
	if err != nil {
		return nil, []JobError{{Code: string(sourceengine.ErrCodeInternal), Message: err.Error(), Details: map[string]any{"source_id": event.SourceID}}}
	}
	if len(bindings) == 0 {
		return nil, []JobError{{Code: string(sourceengine.ErrCodeBindingNotFound), Message: "no active local binding for agent event", Details: map[string]any{"source_id": event.SourceID, "agent_id": agentID}}}
	}
	if strings.TrimSpace(event.BindingID) != "" {
		filtered := bindings[:0]
		for _, binding := range bindings {
			if binding.BindingID == event.BindingID {
				filtered = append(filtered, binding)
			}
		}
		bindings = filtered
		if len(bindings) == 0 {
			return nil, []JobError{{Code: string(sourceengine.ErrCodeBindingNotFound), Message: "binding is not active for agent event", Details: map[string]any{"source_id": event.SourceID, "binding_id": event.BindingID, "agent_id": agentID}}}
		}
	}
	var jobIDs []string
	var errors []JobError
	for _, binding := range bindings {
		intent, err := h.scheduler.EnqueueWatchEventSync(r.Context(), scheduleengine.WatchEventSyncRequest{
			Binding:    binding,
			ObjectKey:  event.ObjectKey,
			Path:       event.Path,
			EventType:  event.EventType,
			OccurredAt: event.OccurredAt,
			IsDir:      event.IsDir,
		})
		if err != nil {
			errors = append(errors, JobError{Code: string(sourceengine.ErrCodeInternal), Message: err.Error(), Details: map[string]any{"binding_id": binding.BindingID}})
			continue
		}
		if intent.Run.RunID != "" {
			jobIDs = append(jobIDs, intent.Run.RunID)
		}
	}
	return jobIDs, errors
}

func agentFromRegister(req agentRegisterRequest, now time.Time) store.Agent {
	return store.Agent{
		AgentID:         strings.TrimSpace(req.AgentID),
		TenantID:        strings.TrimSpace(req.TenantID),
		Hostname:        strings.TrimSpace(req.Hostname),
		Version:         strings.TrimSpace(req.Version),
		Status:          "ONLINE",
		ListenAddr:      strings.TrimSpace(req.ListenAddr),
		LastHeartbeatAt: now,
		UpdatedAt:       now,
	}
}

func agentFromHeartbeat(req agentHeartbeatRequest, now time.Time) store.Agent {
	heartbeatAt := req.LastHeartbeatAt.UTC()
	if heartbeatAt.IsZero() {
		heartbeatAt = now
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "ONLINE"
	}
	return store.Agent{
		AgentID:           strings.TrimSpace(req.AgentID),
		TenantID:          strings.TrimSpace(req.TenantID),
		Hostname:          strings.TrimSpace(req.Hostname),
		Version:           strings.TrimSpace(req.Version),
		Status:            status,
		ListenAddr:        strings.TrimSpace(req.ListenAddr),
		LastHeartbeatAt:   heartbeatAt,
		ActiveSourceCount: req.SourceCount,
		ActiveWatchCount:  req.ActiveWatchCount,
		ActiveTaskCount:   req.ActiveTaskCount,
		UpdatedAt:         now,
	}
}

func agentCommandToResponse(command store.AgentCommand) agentCommandResponse {
	id, _ := strconv.ParseInt(command.CommandID, 10, 64)
	payload := command.Payload
	return agentCommandResponse{
		ID:              id,
		Type:            stringValue(payload, "type", command.CommandType),
		TenantID:        stringValue(payload, "tenant_id", ""),
		SourceID:        stringValue(payload, "source_id", ""),
		BindingID:       stringValue(payload, "binding_id", ""),
		RootPath:        stringValue(payload, agentCommandRootKey(), ""),
		Mode:            stringValue(payload, "mode", ""),
		Reason:          stringValue(payload, "reason", ""),
		SkipInitialScan: boolValue(payload, "skip_initial_scan"),
		DocumentID:      stringValue(payload, "document_id", ""),
		VersionID:       stringValue(payload, "version_id", ""),
		SrcPath:         stringValue(payload, "src_path", ""),
	}
}

func (r agentCommandResponse) MarshalJSON() ([]byte, error) {
	type commandAlias agentCommandResponse
	payload := make(map[string]any)
	body, err := json.Marshal(commandAlias(r))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.RootPath) != "" {
		payload[agentCommandRootKey()] = r.RootPath
	}
	return json.Marshal(payload)
}

func agentCommandRootKey() string {
	return agentCommandRootKeyPrefix + agentCommandRootKeySuffix
}

func decodeAgentResult(raw string) (store.JSON, error) {
	if strings.TrimSpace(raw) == "" {
		return store.JSON{}, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("must be a JSON object")
	}
	return store.JSON(value), nil
}

func stringValue(values store.JSON, key, fallback string) string {
	if values == nil {
		return fallback
	}
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func boolValue(values store.JSON, key string) bool {
	if values == nil {
		return false
	}
	if value, ok := values[key].(bool); ok {
		return value
	}
	return false
}
