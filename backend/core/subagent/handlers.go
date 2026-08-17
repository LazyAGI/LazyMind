package subagent

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

func authorizeWorkflowExecutor(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN"))
	if expected == "" {
		return strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") ||
			strings.HasPrefix(r.RemoteAddr, "[::1]:") || r.RemoteAddr == ""
	}
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if len(provided) == len(expected) && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1 {
		return true
	}
	common.ReplyErr(w, "executor unauthorized", http.StatusUnauthorized)
	return false
}

// InternalGetExecutionSpec returns LazyMind Host-private configuration to the
// authenticated remote LazyMind Executor. Workflow Runtime never stores this
// data in Attempt Context or sends it to another Host.
func InternalGetExecutionSpec(w http.ResponseWriter, r *http.Request) {
	taskID := common.PathVar(r, "task_id")
	if !authorizeWorkflowExecutorTask(w, r, taskID) {
		return
	}
	task, err := GetTask(r.Context(), store.DB(), taskID)
	if err != nil {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}
	config, err := modelconfig.LoadLLMConfig(r.Context(), store.DB(), task.CreateUserID)
	if err != nil {
		common.ReplyErr(w, "model config unavailable", http.StatusServiceUnavailable)
		return
	}
	toolConfig, err := modelconfig.LoadSearchToolConfig(r.Context(), store.DB(), task.CreateUserID)
	if err != nil {
		common.ReplyErr(w, "tool config unavailable", http.StatusServiceUnavailable)
		return
	}
	if toolConfig == nil {
		toolConfig = map[string]any{}
	}
	cloudToolConfig, err := modelconfig.LoadCloudToolConfig(r.Context(), task.CreateUserID)
	if err != nil {
		common.ReplyErr(w, "tool config unavailable", http.StatusServiceUnavailable)
		return
	}
	for name, credential := range cloudToolConfig {
		toolConfig[name] = credential
	}
	steps, _ := LoadSteps(r.Context(), store.DB(), taskID)
	stepDTOs := make([]stepDTO, 0, len(steps))
	for i := range steps {
		stepDTOs = append(stepDTOs, toStepDTO(&steps[i]))
	}
	common.ReplyOK(w, map[string]any{"task": toTaskDTO(task), "params": task.Params,
		"steps": stepDTOs, "create_user_id": task.CreateUserID, "llm_config": config,
		"tool_config": toolConfig, "workspace_path": task.WorkspacePath})
}

// InternalIngestTaskEvent preserves the ordinary LazyMind SubAgent task stream
// when the Workflow Executor runs outside Core. Non-Workflow SubAgents keep
// using the same routeEvent function directly.
func InternalIngestTaskEvent(w http.ResponseWriter, r *http.Request) {
	taskID := common.PathVar(r, "task_id")
	if !authorizeWorkflowExecutorTask(w, r, taskID) {
		return
	}
	var event TaskEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		common.ReplyErr(w, "invalid task event", http.StatusBadRequest)
		return
	}
	event.TaskID = taskID
	if event.Type == "artifact" {
		task, err := GetTask(r.Context(), store.DB(), taskID)
		if err != nil {
			common.ReplyErr(w, "task unavailable", http.StatusServiceUnavailable)
			return
		}
		var params struct {
			OutputTypes map[string]string `json:"output_slot_types"`
		}
		_ = json.Unmarshal(task.Params, &params)
		if params.OutputTypes[event.ArtifactKey] == "file" && event.ContentType != "file" && event.ContentType != "file_list" {
			common.ReplyErr(w, "file slot requires file or file_list content type", http.StatusUnprocessableEntity)
			return
		}
	}
	if role, content := remoteStepContent(event); role != "" {
		if err := AppendRemoteStep(r.Context(), store.DB(), taskID, role, content); err != nil {
			common.ReplyErr(w, "persist task event failed", http.StatusServiceUnavailable)
			return
		}
	}
	// Artifacts are committed through the fenced remote Workflow API. Terminal
	// hooks remain enabled after Runtime terminal commit so LazyMind conversation
	// handoff/synthetic-turn behavior remains identical to the in-process path.
	if err := routeEventWithWorkflowHooks(r.Context(), store.DB(), store.State(), event, false, true); err != nil {
		common.ReplyErr(w, "persist task event failed", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, map[string]any{"accepted": true})
}

func authorizeWorkflowExecutorTask(w http.ResponseWriter, r *http.Request, taskID string) bool {
	if !authorizeWorkflowExecutor(w, r) {
		return false
	}
	lease := strings.TrimSpace(r.Header.Get("X-Workflow-Lease-Token"))
	if lease == "" {
		common.ReplyErr(w, "executor unauthorized", http.StatusUnauthorized)
		return false
	}
	var row orm.WorkflowSessionStep
	if err := store.DB().WithContext(r.Context()).Where("task_id = ? AND lease_token = ?", taskID, lease).
		Where("(lease_expires_at >= ? AND status IN ?) OR status IN ?", time.Now().UTC(),
			[]string{"claimed", "running"}, []string{"succeeded", "failed", "cancelled", "interrupted"}).
		First(&row).Error; err != nil {
		common.ReplyErr(w, "executor unauthorized", http.StatusConflict)
		return false
	}
	return true
}

func remoteStepContent(event TaskEvent) (string, json.RawMessage) {
	var value any
	role := ""
	switch event.Type {
	case "text":
		role, value = "text", map[string]any{"content": event.Text}
	case "think":
		role, value = "think", map[string]any{"content": event.Think}
	case "tool_calls":
		role, value = "assistant", map[string]any{"tool_calls": event.ToolCalls}
	case "tool_results":
		role, value = "tool", map[string]any{"tool_results": event.ToolResults}
	}
	if role == "" {
		return "", nil
	}
	raw, _ := json.Marshal(value)
	return role, raw
}

// taskDTO is the JSON shape returned to the frontend for a task.
type taskDTO struct {
	TaskID           string          `json:"task_id"`
	ConversationID   string          `json:"conversation_id"`
	TriggerHistoryID string          `json:"trigger_history_id"`
	Seq              int             `json:"seq_in_conversation"`
	AgentType        string          `json:"agent_type"`
	Title            string          `json:"title"`
	Objective        string          `json:"objective"`
	Mode             string          `json:"mode"`
	Status           string          `json:"status"`
	Progress         int             `json:"progress_pct"`
	CurrentPhase     string          `json:"current_phase"`
	EstimatedSec     int             `json:"estimated_sec"`
	Summary          string          `json:"summary"`
	InputSlots       json.RawMessage `json:"input_slots"`
	OutputSlots      json.RawMessage `json:"output_slots"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Artifacts        []artifactDTO   `json:"artifacts,omitempty"`
	Steps            []stepDTO       `json:"steps,omitempty"`
}

type taskProgressDTO struct {
	TaskID       string    `json:"task_id"`
	Seq          int       `json:"seq_in_conversation"`
	AgentType    string    `json:"agent_type"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	Progress     int       `json:"progress_pct"`
	CurrentPhase string    `json:"current_phase"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type stepDTO struct {
	Seq     int             `json:"seq"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type artifactDTO struct {
	Slot        string          `json:"slot"`
	ContentType string          `json:"content_type"`
	Seq         int             `json:"seq"`
	Value       json.RawMessage `json:"value"`
	CreatedAt   time.Time       `json:"created_at"`
}

func toTaskDTO(t *orm.SubAgentTask) taskDTO {
	return taskDTO{
		TaskID:           t.ID,
		ConversationID:   t.ConversationID,
		TriggerHistoryID: t.TriggerHistoryID,
		Seq:              t.SeqInConversation,
		AgentType:        t.AgentType,
		Title:            t.Title,
		Objective:        t.Objective,
		Mode:             t.Mode,
		Status:           t.Status,
		Progress:         t.ProgressPct,
		CurrentPhase:     t.CurrentPhase,
		EstimatedSec:     t.EstimatedSec,
		Summary:          t.Summary,
		InputSlots:       normalizeJSON(t.InputSlots, "[]"),
		OutputSlots:      normalizeJSON(t.OutputSlots, "[]"),
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
	}
}

func toArtifactDTO(a *orm.SubAgentArtifact, workspacePath string) artifactDTO {
	value := normalizeJSON(a.Value, "{}")
	value = SignArtifactValue(a.ContentType, value, workspacePath)
	return artifactDTO{
		Slot:        a.Slot,
		ContentType: a.ContentType,
		Seq:         a.Seq,
		Value:       value,
		CreatedAt:   a.CreatedAt,
	}
}

func toStepDTO(s *orm.SubAgentStep) stepDTO {
	return stepDTO{
		Seq:     s.Seq,
		Role:    s.Role,
		Content: normalizeJSON(s.Content, "{}"),
	}
}

// ListConversationTasks handles GET /conversations/{conversation_id}/tasks.
func ListConversationTasks(w http.ResponseWriter, r *http.Request) {
	convID := common.PathVar(r, "conversation_id")
	if convID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	userID := requestUserID(r)
	tasks, err := ListTasksByConversationForUser(ctx, db, convID, userID)
	if err != nil {
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	summaryOnly := r.URL.Query().Get("summary_only") == "true"
	if summaryOnly {
		out := make([]taskProgressDTO, 0, len(tasks))
		for i := range tasks {
			out = append(out, taskProgressDTO{
				TaskID:       tasks[i].ID,
				Seq:          tasks[i].SeqInConversation,
				AgentType:    tasks[i].AgentType,
				Title:        tasks[i].Title,
				Status:       tasks[i].Status,
				Progress:     tasks[i].ProgressPct,
				CurrentPhase: tasks[i].CurrentPhase,
				UpdatedAt:    tasks[i].UpdatedAt,
			})
		}
		common.ReplyOK(w, map[string]any{"tasks": out})
		return
	}
	out := make([]taskDTO, 0, len(tasks))
	for i := range tasks {
		dto := toTaskDTO(&tasks[i])
		arts, err := LoadArtifacts(ctx, db, tasks[i].ID)
		if err != nil {
			common.ReplyErr(w, "query task artifacts failed", http.StatusInternalServerError)
			return
		}
		for j := range arts {
			if !arts[j].Hidden {
				dto.Artifacts = append(dto.Artifacts, toArtifactDTO(&arts[j], tasks[i].WorkspacePath))
			}
		}
		steps, _ := LoadSteps(ctx, db, tasks[i].ID)
		for j := range steps {
			dto.Steps = append(dto.Steps, toStepDTO(&steps[j]))
		}
		out = append(out, dto)
	}
	common.ReplyOK(w, map[string]any{"tasks": out})
}

// GetTaskDetail handles GET /tasks/{task_id}.
func GetTaskDetail(w http.ResponseWriter, r *http.Request) {
	taskID := common.PathVar(r, "task_id")
	if taskID == "" {
		common.ReplyErr(w, "task_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	t, err := GetTask(ctx, db, taskID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query task failed", http.StatusInternalServerError)
		return
	}
	if t.CreateUserID != requestUserID(r) {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}
	dto := toTaskDTO(t)
	stepCount, _ := CountSteps(ctx, db, taskID)
	common.ReplyOK(w, map[string]any{"task": dto, "step_count": stepCount})
}

// GetTaskArtifacts handles GET /tasks/{task_id}/artifacts.
func GetTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	taskID := common.PathVar(r, "task_id")
	if taskID == "" {
		common.ReplyErr(w, "task_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	task, err := GetTask(r.Context(), db, taskID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
		} else {
			common.ReplyErr(w, "query task failed", http.StatusInternalServerError)
		}
		return
	}
	if task.CreateUserID != requestUserID(r) {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}
	arts, err := LoadArtifacts(r.Context(), db, taskID)
	if err != nil {
		common.ReplyErr(w, "query artifacts failed", http.StatusInternalServerError)
		return
	}
	out := make([]artifactDTO, 0, len(arts))
	for i := range arts {
		if !arts[i].Hidden {
			out = append(out, toArtifactDTO(&arts[i], task.WorkspacePath))
		}
	}
	common.ReplyOK(w, map[string]any{"artifacts": out})
}

func requestUserID(r *http.Request) string {
	userID := store.UserID(r)
	if userID == "" {
		return "0"
	}
	return userID
}

// InternalGetTaskEvents handles GET /internal/subagent/tasks/{task_id}/events?from={offset}
// for Python auto polling. Returns a batch of raw task stream events from the given offset.
// The caller increments the offset by the number of events returned to paginate forward.
func InternalGetTaskEvents(w http.ResponseWriter, r *http.Request) {
	taskID := common.PathVar(r, "task_id")
	if taskID == "" {
		common.ReplyErr(w, "task_id required", http.StatusBadRequest)
		return
	}
	from := int64(0)
	if s := r.URL.Query().Get("from"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			from = n
		}
	}
	stateStore := store.State()
	ctx := r.Context()
	raws, err := StreamEventsFrom(ctx, stateStore, taskID, from)
	if err != nil {
		common.ReplyErr(w, "read events failed", http.StatusInternalServerError)
		return
	}
	events := make([]json.RawMessage, 0, len(raws))
	for _, raw := range raws {
		events = append(events, json.RawMessage(raw))
	}
	common.ReplyOK(w, map[string]any{"events": events, "next_from": from + int64(len(raws))})
}

// InternalGetTaskStatus handles GET /internal/subagent/tasks/{task_id} for Python auto polling.
// Prefers the Redis status snapshot, falling back to the DB row.
func InternalGetTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := common.PathVar(r, "task_id")
	if taskID == "" {
		common.ReplyErr(w, "task_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	stateStore := store.State()
	if snap, err := ReadStatus(ctx, stateStore, taskID); err == nil && len(snap) > 0 {
		resp := map[string]any{
			"task_id":       taskID,
			"status":        snap["status"],
			"current_phase": snap["current_phase"],
			"summary":       snap["summary"],
		}
		if p, ok := snap["progress"]; ok {
			resp["progress"] = p
		}
		common.ReplyOK(w, resp)
		return
	}
	t, err := GetTask(ctx, db, taskID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query task failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"task_id":       t.ID,
		"status":        t.Status,
		"progress":      t.ProgressPct,
		"current_phase": t.CurrentPhase,
		"summary":       t.Summary,
	})
}
