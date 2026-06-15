package subagent

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// taskDTO is the JSON shape returned to the frontend for a task.
type taskDTO struct {
	TaskID             string          `json:"task_id"`
	ConversationID     string          `json:"conversation_id"`
	TriggerHistoryID   string          `json:"trigger_history_id"`
	Seq                int             `json:"seq_in_conversation"`
	AgentType          string          `json:"agent_type"`
	Title              string          `json:"title"`
	Objective          string          `json:"objective"`
	Mode               string          `json:"mode"`
	Status             string          `json:"status"`
	Progress           int             `json:"progress_pct"`
	CurrentPhase       string          `json:"current_phase"`
	EstimatedSec       int             `json:"estimated_sec"`
	Summary            string          `json:"summary"`
	InputArtifactKeys  json.RawMessage `json:"input_artifact_keys"`
	OutputArtifactKeys json.RawMessage `json:"output_artifact_keys"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	Artifacts          []artifactDTO   `json:"artifacts,omitempty"`
	Steps              []stepDTO       `json:"steps,omitempty"`
}

type stepDTO struct {
	Seq     int             `json:"seq"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type artifactDTO struct {
	ArtifactKey string          `json:"artifact_key"`
	ContentType string          `json:"content_type"`
	Seq         int             `json:"seq"`
	Value       json.RawMessage `json:"value"`
	CreatedAt   time.Time       `json:"created_at"`
}

func toTaskDTO(t *orm.SubAgentTask) taskDTO {
	return taskDTO{
		TaskID:             t.ID,
		ConversationID:     t.ConversationID,
		TriggerHistoryID:   t.TriggerHistoryID,
		Seq:                t.SeqInConversation,
		AgentType:          t.AgentType,
		Title:              t.Title,
		Objective:          t.Objective,
		Mode:               t.Mode,
		Status:             t.Status,
		Progress:           t.ProgressPct,
		CurrentPhase:       t.CurrentPhase,
		EstimatedSec:       t.EstimatedSec,
		Summary:            t.Summary,
		InputArtifactKeys:  normalizeJSON(t.InputArtifactKeys, "[]"),
		OutputArtifactKeys: normalizeJSON(t.OutputArtifactKeys, "[]"),
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
	}
}

func toArtifactDTO(a *orm.SubAgentArtifact) artifactDTO {
	return artifactDTO{
		ArtifactKey: a.ArtifactKey,
		ContentType: a.ContentType,
		Seq:         a.Seq,
		Value:       normalizeJSON(a.Value, "{}"),
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
	tasks, err := ListTasksByConversation(ctx, db, convID)
	if err != nil {
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	out := make([]taskDTO, 0, len(tasks))
	for i := range tasks {
		dto := toTaskDTO(&tasks[i])
		arts, _ := LoadArtifacts(ctx, db, tasks[i].ID)
		for j := range arts {
			dto.Artifacts = append(dto.Artifacts, toArtifactDTO(&arts[j]))
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
	arts, err := LoadArtifacts(r.Context(), db, taskID)
	if err != nil {
		common.ReplyErr(w, "query artifacts failed", http.StatusInternalServerError)
		return
	}
	out := make([]artifactDTO, 0, len(arts))
	for i := range arts {
		out = append(out, toArtifactDTO(&arts[i]))
	}
	common.ReplyOK(w, map[string]any{"artifacts": out})
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
	rdb := store.Redis()
	ctx := r.Context()
	raws, err := StreamEventsFrom(ctx, rdb, taskID, from)
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
	rdb := store.Redis()
	if snap, err := ReadStatus(ctx, rdb, taskID); err == nil && len(snap) > 0 {
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
