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

func authorizeInternalService(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	provided := strings.TrimSpace(r.Header.Get("X-LazyMind-Internal-Token"))
	if expected == "" || provided == "" || len(provided) != len(expected) ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		common.ReplyErr(w, "internal token required", http.StatusUnauthorized)
		return false
	}
	return true
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
	var runtimeParams struct {
		LegacyTools []string `json:"legacy_tools"`
	}
	if len(task.Params) > 0 {
		if err := json.Unmarshal(task.Params, &runtimeParams); err != nil {
			common.ReplyErr(w, "tool config unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	toolConfig, err := modelconfig.LoadToolConfigForCapabilities(
		r.Context(), store.DB(), task.CreateUserID, runtimeParams.LegacyTools,
	)
	if err != nil {
		common.ReplyErr(w, "tool config unavailable", http.StatusServiceUnavailable)
		return
	}
	if toolConfig == nil {
		toolConfig = map[string]any{}
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
	event.DurableToolResults = nil
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
		var calls []map[string]any
		if json.Unmarshal(event.ToolCalls, &calls) != nil {
			calls = []map[string]any{}
		}
		role, value = "assistant", map[string]any{"text": "", "tool_calls": calls}
	case "tool_results":
		var incoming []map[string]any
		persistedResults := event.DurableToolResults
		if len(persistedResults) == 0 {
			persistedResults = event.ToolResults
		}
		if json.Unmarshal(persistedResults, &incoming) != nil {
			incoming = []map[string]any{}
		}
		results := make([]map[string]any, 0, len(incoming))
		for _, result := range incoming {
			toolCallID := result["tool_call_id"]
			if toolCallID == nil {
				toolCallID = result["id"]
			}
			resultValue := result["result"]
			if resultValue == nil {
				resultValue = result["content"]
			}
			results = append(results, map[string]any{
				"tool_call_id": toolCallID,
				"name":         result["name"],
				"result":       resultValue,
			})
		}
		role, value = "tool", map[string]any{"tool_results": results}
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
	Query            string          `json:"query,omitempty"`
	Objective        string          `json:"objective"`
	Mode             string          `json:"mode"`
	Status           string          `json:"status"`
	Progress         int             `json:"progress_pct"`
	CurrentPhase     string          `json:"current_phase"`
	EstimatedSec     int             `json:"estimated_sec"`
	Summary          string          `json:"summary"`
	InputSlots       json.RawMessage `json:"input_slots"`
	OutputSlots      json.RawMessage `json:"output_slots"`
	Sources          json.RawMessage `json:"sources"`
	WritingSubtasks  json.RawMessage `json:"writing_subtasks"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Artifacts        []artifactDTO   `json:"artifacts,omitempty"`
	Steps            []stepDTO       `json:"steps,omitempty"`
}

// TaskDisplayQuery returns only the user-authored query suitable for TaskCenter.
// Workflow objectives contain the expanded step/system prompt and must never be
// used as a display fallback.
func TaskDisplayQuery(t *orm.SubAgentTask) string {
	var params struct {
		UserInput string `json:"user_input"`
		Query     string `json:"query"`
	}
	_ = json.Unmarshal(t.Params, &params)
	if query := strings.TrimSpace(params.UserInput); query != "" {
		return query
	}
	if query := strings.TrimSpace(params.Query); query != "" {
		return query
	}
	if t.AgentType != "workflow_step" {
		return strings.TrimSpace(t.Objective)
	}
	return ""
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

type internalArtifactDTO struct {
	TaskID string `json:"task_id"`
	artifactDTO
}

func toTaskDTO(t *orm.SubAgentTask) taskDTO {
	return taskDTO{
		TaskID:           t.ID,
		ConversationID:   t.ConversationID,
		TriggerHistoryID: t.TriggerHistoryID,
		Seq:              t.SeqInConversation,
		AgentType:        t.AgentType,
		Title:            t.Title,
		Query:            TaskDisplayQuery(t),
		Objective:        t.Objective,
		Mode:             t.Mode,
		Status:           t.Status,
		Progress:         t.ProgressPct,
		CurrentPhase:     t.CurrentPhase,
		EstimatedSec:     t.EstimatedSec,
		Summary:          t.Summary,
		InputSlots:       normalizeJSON(t.InputSlots, "[]"),
		OutputSlots:      normalizeJSON(t.OutputSlots, "[]"),
		Sources:          normalizeJSON(json.RawMessage(t.Sources), "[]"),
		WritingSubtasks:  normalizeJSON(json.RawMessage(t.WritingSubtasks), "[]"),
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

// InternalListConversationTasks is the service boundary used by Algorithm
// processes. It deliberately returns task DTOs instead of exposing SQL or the
// core.db path.
func InternalListConversationTasks(w http.ResponseWriter, r *http.Request) {
	if !authorizeInternalService(w, r) {
		return
	}
	conversationID := strings.TrimSpace(common.PathVar(r, "conversation_id"))
	if conversationID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	tasks, err := ListTasksByConversation(r.Context(), store.DB(), conversationID)
	if err != nil {
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	out := make([]taskDTO, 0, len(tasks))
	for i := range tasks {
		out = append(out, toTaskDTO(&tasks[i]))
	}
	common.ReplyOK(w, map[string]any{"tasks": out})
}

// InternalGetTaskArtifacts returns visible task artifacts to trusted Algorithm
// callers without granting them direct access to Core persistence.
func InternalGetTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	if !authorizeInternalService(w, r) {
		return
	}
	taskID := strings.TrimSpace(common.PathVar(r, "task_id"))
	if taskID == "" {
		common.ReplyErr(w, "task_id required", http.StatusBadRequest)
		return
	}
	task, err := GetTask(r.Context(), store.DB(), taskID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
		} else {
			common.ReplyErr(w, "query task failed", http.StatusInternalServerError)
		}
		return
	}
	artifacts, err := LoadArtifacts(r.Context(), store.DB(), taskID)
	if err != nil {
		common.ReplyErr(w, "query artifacts failed", http.StatusInternalServerError)
		return
	}
	out := make([]artifactDTO, 0, len(artifacts))
	for i := range artifacts {
		if !artifacts[i].Hidden {
			out = append(out, toArtifactDTO(&artifacts[i], task.WorkspacePath))
		}
	}
	common.ReplyOK(w, map[string]any{"artifacts": out})
}

// InternalGetTaskArtifactsBatch performs one bounded read for chat context
// construction, avoiding one HTTP request and one SQL query per task.
func InternalGetTaskArtifactsBatch(w http.ResponseWriter, r *http.Request) {
	if !authorizeInternalService(w, r) {
		return
	}
	taskIDs := r.URL.Query()["task_id"]
	if len(taskIDs) == 0 {
		common.ReplyOK(w, map[string]any{"artifacts": []internalArtifactDTO{}})
		return
	}
	if len(taskIDs) > 100 {
		common.ReplyErr(w, "at most 100 task_id values are allowed", http.StatusBadRequest)
		return
	}
	normalized := make([]string, 0, len(taskIDs))
	seen := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		if _, exists := seen[taskID]; exists {
			continue
		}
		seen[taskID] = struct{}{}
		normalized = append(normalized, taskID)
	}
	if len(normalized) == 0 {
		common.ReplyOK(w, map[string]any{"artifacts": []internalArtifactDTO{}})
		return
	}

	var tasks []orm.SubAgentTask
	if err := store.DB().WithContext(r.Context()).Select("id", "workspace_path").
		Where("id IN ?", normalized).Find(&tasks).Error; err != nil {
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	workspaceByTask := make(map[string]string, len(tasks))
	for i := range tasks {
		workspaceByTask[tasks[i].ID] = tasks[i].WorkspacePath
	}
	var artifacts []orm.SubAgentArtifact
	if err := store.DB().WithContext(r.Context()).Where("task_id IN ? AND hidden = ?", normalized, false).
		Order("task_id ASC, slot ASC, seq ASC").Find(&artifacts).Error; err != nil {
		common.ReplyErr(w, "query artifacts failed", http.StatusInternalServerError)
		return
	}
	out := make([]internalArtifactDTO, 0, len(artifacts))
	for i := range artifacts {
		out = append(out, internalArtifactDTO{
			TaskID:      artifacts[i].TaskID,
			artifactDTO: toArtifactDTO(&artifacts[i], workspaceByTask[artifacts[i].TaskID]),
		})
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
