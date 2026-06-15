package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

// sessionDTO is the frontend shape for a PluginSession.
type sessionDTO struct {
	SessionID      string    `json:"session_id"`
	ConversationID string    `json:"conversation_id"`
	PluginID       string    `json:"plugin_id"`
	Status         string    `json:"status"`
	CurrentStepID  string    `json:"current_step_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Slots          []slotDTO `json:"slots,omitempty"`
	Steps          []stepDTO `json:"steps,omitempty"`
}

// stepDTO summarises one plugin_session_steps row (used for dependency validation).
type stepDTO struct {
	StepID    string    `json:"step_id"`
	Attempt   int       `json:"attempt"`
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// slotDTO represents a currently-selected slot revision.
type slotDTO struct {
	SlotID      string    `json:"slot_id"`
	Revision    int       `json:"revision"`
	ListIndex   *int      `json:"list_index,omitempty"`
	Selected    bool      `json:"selected"`
	ArtifactKey string    `json:"artifact_key"`
	StepID      string    `json:"step_id"`
	Attempt     int       `json:"attempt"`
	CreatedAt   time.Time `json:"created_at"`
}

func toSessionDTO(s *orm.PluginSession) sessionDTO {
	return sessionDTO{
		SessionID:      s.ID,
		ConversationID: s.ConversationID,
		PluginID:       s.PluginID,
		Status:         s.Status,
		CurrentStepID:  s.CurrentStepID,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

func toStepDTO(r *orm.PluginSessionStep) stepDTO {
	return stepDTO{
		StepID:    r.StepID,
		Attempt:   r.Attempt,
		TaskID:    r.TaskID,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
	}
}

func toSlotDTO(r *orm.PluginSlotRevision) slotDTO {
	return slotDTO{
		SlotID:      r.SlotID,
		Revision:    r.Revision,
		ListIndex:   r.ListIndex,
		Selected:    r.Selected,
		ArtifactKey: r.ArtifactKey,
		StepID:      r.StepID,
		Attempt:     r.Attempt,
		CreatedAt:   r.CreatedAt,
	}
}

// ListConversationSessions handles GET /conversations/{conversation_id}/plugin-sessions.
func ListConversationSessions(w http.ResponseWriter, r *http.Request) {
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
	sessions, err := ListSessions(r.Context(), db, convID)
	if err != nil {
		common.ReplyErr(w, "query sessions failed", http.StatusInternalServerError)
		return
	}
	out := make([]sessionDTO, 0, len(sessions))
	for i := range sessions {
		out = append(out, toSessionDTO(&sessions[i]))
	}
	common.ReplyOK(w, map[string]any{"sessions": out})
}

// GetSessionDetail handles GET /plugin-sessions/{session_id}.
func GetSessionDetail(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	s, err := GetSession(ctx, db, sessionID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	dto := toSessionDTO(s)
	// Load slots inline.
	revisions, _ := LoadSelectedSlots(ctx, db, sessionID)
	for i := range revisions {
		dto.Slots = append(dto.Slots, toSlotDTO(&revisions[i]))
	}
	// Load steps inline (used by Python Layer-2 dependency validation).
	steps, _ := ListSteps(ctx, db, sessionID)
	for i := range steps {
		dto.Steps = append(dto.Steps, toStepDTO(&steps[i]))
	}
	common.ReplyOK(w, map[string]any{"session": dto})
}

// GetSessionSlots handles GET /plugin-sessions/{session_id}/slots.
func GetSessionSlots(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	revisions, err := LoadSelectedSlots(r.Context(), db, sessionID)
	if err != nil {
		common.ReplyErr(w, "query slots failed", http.StatusInternalServerError)
		return
	}
	out := make([]slotDTO, 0, len(revisions))
	for i := range revisions {
		out = append(out, toSlotDTO(&revisions[i]))
	}
	common.ReplyOK(w, map[string]any{"slots": out})
}

// PatchSessionSlot handles PATCH /plugin-sessions/{session_id}/slots/{slot_id}.
// Accepts body: {"selected_revision": int} to switch which revision is displayed.
func PatchSessionSlot(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	if sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id and slot_id required", http.StatusBadRequest)
		return
	}
	var body struct {
		SelectedRevision int `json:"selected_revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	// Deselect all, then select the target revision.
	if err := db.WithContext(ctx).Model(&orm.PluginSlotRevision{}).
		Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true).
		Update("selected", false).Error; err != nil {
		common.ReplyErr(w, "update slot failed", http.StatusInternalServerError)
		return
	}
	if err := db.WithContext(ctx).Model(&orm.PluginSlotRevision{}).
		Where("session_id = ? AND slot_id = ? AND revision = ?", sessionID, slotID, body.SelectedRevision).
		Update("selected", true).Error; err != nil {
		common.ReplyErr(w, "select revision failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"selected_revision": body.SelectedRevision})
}

// GetActiveConversationSession handles GET /conversations/{conversation_id}/plugin-sessions:active.
func GetActiveConversationSession(w http.ResponseWriter, r *http.Request) {
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
	s, err := GetActiveSession(r.Context(), db, convID)
	if err != nil {
		common.ReplyErr(w, "query active session failed", http.StatusInternalServerError)
		return
	}
	if s == nil {
		common.ReplyOK(w, map[string]any{"session": nil})
		return
	}
	dto := toSessionDTO(s)
	revisions, _ := LoadSelectedSlots(r.Context(), db, s.ID)
	for i := range revisions {
		dto.Slots = append(dto.Slots, toSlotDTO(&revisions[i]))
	}
	common.ReplyOK(w, map[string]any{"session": dto})
}

// AdvanceSession handles POST /plugin-sessions/{session_id}:advance.
// This is the §5.5 manual-mode resume path: the frontend calls this after
// the user confirms they want to proceed. Go inspects the last step status
// and takes the appropriate action (wait / resume interrupted / trigger ChatAgent).
func AdvanceSession(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	rdb := store.Redis()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()

	session, err := GetSession(ctx, db, sessionID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	if session.Status != SessionStatusWaiting && session.Status != SessionStatusActive {
		common.ReplyErr(w, "session is not in a resumable state", http.StatusConflict)
		return
	}

	// Find the latest step for the current step_id.
	step, err := GetLatestStep(ctx, db, sessionID, session.CurrentStepID)
	if err != nil || step == nil {
		common.ReplyErr(w, "no step found for current_step_id", http.StatusInternalServerError)
		return
	}

	userID := store.UserID(r)

	switch step.Status {
	case StepStatusRunning:
		// Step is still running (heartbeat not timed out); nothing to do.
		common.ReplyOK(w, map[string]any{"action": "waiting", "message": "step is still running"})

	case StepStatusInterrupted:
		// Resume the interrupted SubAgent directly, bypassing ChatAgent.
		_ = UpdateSessionStatus(ctx, db, sessionID, SessionStatusActive)
		task, tErr := subagent.GetTask(ctx, db, step.TaskID)
		if tErr != nil {
			common.ReplyErr(w, "fetch task failed", http.StatusInternalServerError)
			return
		}
		var params PluginStepParams
		if len(task.Params) > 0 {
			_ = json.Unmarshal(task.Params, &params)
		}
		var inputKeys, outputKeys []string
		if len(task.InputArtifactKeys) > 0 {
			_ = json.Unmarshal(task.InputArtifactKeys, &inputKeys)
		}
		if len(task.OutputArtifactKeys) > 0 {
			_ = json.Unmarshal(task.OutputArtifactKeys, &outputKeys)
		}
		// LLMConfig is not persisted on the task; subagent runner uses its default model on resume.
		go subagent.Run(context.Background(), db, rdb, subagent.RunRequest{
			TaskID:             task.ID,
			AgentType:          "plugin_step",
			Objective:          task.Objective,
			Params:             params.asMap(),
			InputArtifactKeys:  inputKeys,
			OutputArtifactKeys: outputKeys,
			WorkspacePath:      task.WorkspacePath,
			Resume:             true,
		})
		common.ReplyOK(w, map[string]any{"action": "resumed", "task_id": task.ID})

	case StepStatusSucceeded:
		// Step already succeeded; synthesise a "user confirmed" message into ChatAgent via Go core.
		syntheticMsg := fmt.Sprintf("Step %s completed. User confirmed. Please proceed.", session.CurrentStepID)
		_ = UpdateSessionStatus(ctx, db, sessionID, SessionStatusActive)
		go triggerNextChatTurn(
			session.ConversationID, sessionID, session.PluginID,
			session.CurrentStepID, userID, syntheticMsg,
		)
		common.ReplyOK(w, map[string]any{"action": "advancing", "message": syntheticMsg})

	default:
		common.ReplyErr(w, fmt.Sprintf("step status %q is not resumable", step.Status), http.StatusConflict)
	}
}
