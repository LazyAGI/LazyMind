package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
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

// slotDTO represents a currently-selected slot revision, with its artifact value inline.
type slotDTO struct {
	SlotID        string          `json:"slot_id"`
	Revision      int             `json:"revision"`
	ListIndex     *int            `json:"list_index,omitempty"`
	Selected      bool            `json:"selected"`
	ArtifactKey   string          `json:"artifact_key"`
	StepID        string          `json:"step_id"`
	Attempt       int             `json:"attempt"`
	CreatedAt     time.Time       `json:"created_at"`
	ContentType   string          `json:"content_type,omitempty"`
	ArtifactValue json.RawMessage `json:"artifact_value,omitempty"`
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

// enrichSlots fills ContentType and ArtifactValue on each slotDTO by looking up
// the corresponding artifact row.
// For each revision: look up plugin_session_steps → task_id, then query
// sub_agent_artifacts(task_id, artifact_key) ordered by seq ASC and pick the
// row at position list_index (0-based); for single slots take the latest (seq DESC).
func enrichSlots(ctx context.Context, db *gorm.DB, sessionID string, slots []slotDTO) {
	// Step 1: build a map (step_id, attempt) → task_id
	type stepKey struct {
		stepID  string
		attempt int
	}
	taskIDByStep := map[stepKey]string{}
	var steps []orm.PluginSessionStep
	db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&steps)
	for _, s := range steps {
		taskIDByStep[stepKey{s.StepID, s.Attempt}] = s.TaskID
	}

	// Step 2: collect distinct task_ids we need artifacts for
	type artifactEntry struct {
		ContentType string
		Value       json.RawMessage
	}
	// key: taskID + "#" + artifactKey → ordered list of artifacts by seq ASC
	artifactsByTask := map[string][]orm.SubAgentArtifact{}
	taskIDs := map[string]bool{}
	for _, slot := range slots {
		tid := taskIDByStep[stepKey{slot.StepID, slot.Attempt}]
		if tid != "" {
			taskIDs[tid] = true
		}
	}
	if len(taskIDs) > 0 {
		ids := make([]string, 0, len(taskIDs))
		for id := range taskIDs {
			ids = append(ids, id)
		}
		var arts []orm.SubAgentArtifact
		db.WithContext(ctx).
			Where("task_id IN ?", ids).
			Order("task_id ASC, artifact_key ASC, seq ASC").
			Find(&arts)
		for _, a := range arts {
			k := a.TaskID + "#" + a.ArtifactKey
			artifactsByTask[k] = append(artifactsByTask[k], a)
		}
	}

	// Step 3: assign value to each slotDTO
	for i := range slots {
		slot := &slots[i]
		tid := taskIDByStep[stepKey{slot.StepID, slot.Attempt}]
		if tid == "" {
			continue
		}
		k := tid + "#" + slot.ArtifactKey
		arts := artifactsByTask[k]
		if len(arts) == 0 {
			continue
		}
		var chosen *orm.SubAgentArtifact
		if slot.ListIndex != nil {
			idx := *slot.ListIndex
			if idx < len(arts) {
				chosen = &arts[idx]
			} else {
				chosen = &arts[len(arts)-1]
			}
		} else {
			// single slot: latest seq
			chosen = &arts[len(arts)-1]
		}
		slot.ContentType = chosen.ContentType
		slot.ArtifactValue = chosen.Value
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
	enrichSlots(ctx, db, sessionID, dto.Slots)
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
	enrichSlots(r.Context(), db, sessionID, out)
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
	enrichSlots(r.Context(), db, s.ID, dto.Slots)
	common.ReplyOK(w, map[string]any{"session": dto})
}

// GetLatestConversationSession handles GET /conversations/{conversation_id}/plugin-sessions:latest.
// Returns the most recent session regardless of status, so the frontend can always show
// plugin output even after a session completes or fails.
func GetLatestConversationSession(w http.ResponseWriter, r *http.Request) {
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
	s, err := GetLatestSession(r.Context(), db, convID)
	if err != nil {
		common.ReplyErr(w, "query latest session failed", http.StatusInternalServerError)
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
	enrichSlots(r.Context(), db, s.ID, dto.Slots)
	common.ReplyOK(w, map[string]any{"session": dto})
}

// GetPluginInfo handles GET /plugins/{plugin_id}.
// Proxies to the Python chat service /api/plugins/{plugin_id} and returns the plugin spec
// including the ui.tabs declaration needed by the frontend PluginPanel.
func GetPluginInfo(w http.ResponseWriter, r *http.Request) {
	pluginID := common.PathVar(r, "plugin_id")
	if pluginID == "" {
		common.ReplyErr(w, "plugin_id required", http.StatusBadRequest)
		return
	}
	upstream := common.ChatServiceEndpoint() + "/api/plugins/" + pluginID
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		common.ReplyErr(w, "build upstream request failed", http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		common.ReplyErr(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	if resp.StatusCode != http.StatusOK {
		common.ReplyErr(w, "upstream error", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
}

// ListPlugins handles GET /plugins.
// Proxies to the Python chat service /api/plugins.
func ListPlugins(w http.ResponseWriter, r *http.Request) {
	upstream := common.ChatServiceEndpoint() + "/api/plugins"
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		common.ReplyErr(w, "build upstream request failed", http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		common.ReplyErr(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		common.ReplyErr(w, "upstream error", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
}

// AdvanceSession handles POST /plugin-sessions/{session_id}:advance.
// This is the §5.5 manual-mode resume path: the frontend calls this after
// the user confirms they want to proceed or retry the current step.
//
// Body (optional): {"action": "continue"|"retry"}  — defaults to "continue".
//   - "continue": proceed to the next step after the current one succeeds.
//   - "retry":    re-run the current step from scratch (full retry via self-loop).
func AdvanceSession(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}

	var body struct {
		Action string `json:"action"` // "continue" | "retry"; default "continue"
	}
	// Ignore decode errors — body is optional; default action is "continue".
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Action == "" {
		body.Action = "continue"
	}
	if body.Action != "continue" && body.Action != "retry" {
		common.ReplyErr(w, `action must be "continue" or "retry"`, http.StatusBadRequest)
		return
	}

	db := store.DB()
	stateStore := store.State()
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
	// completed sessions can be retried (re-run a step), but not continued.
	if session.Status == SessionStatusCompleted {
		if body.Action != "retry" {
			common.ReplyErr(w, "completed sessions can only be retried, not continued", http.StatusConflict)
			return
		}
		// Reset to active so the state machine can proceed.
		if err := UpdateSessionStatus(ctx, db, sessionID, SessionStatusActive); err != nil {
			common.ReplyErr(w, "reset session status failed", http.StatusInternalServerError)
			return
		}
		session.Status = SessionStatusActive
	} else if session.Status != SessionStatusWaiting && session.Status != SessionStatusActive {
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
		// LLMConfig is not persisted on the task; subagent runner uses its default model on resume.
		// input_artifact_keys, output_artifact_keys, and tools are read by the Python runner from DB.
		go subagent.Run(context.Background(), db, stateStore, subagent.RunRequest{
			TaskID:        task.ID,
			AgentType:     "plugin_step",
			Params:        params.asMap(),
			WorkspacePath: task.WorkspacePath,
			Resume:        true,
		})
		common.ReplyOK(w, map[string]any{"action": "resumed", "task_id": task.ID})

	case StepStatusSucceeded:
		_ = UpdateSessionStatus(ctx, db, sessionID, SessionStatusActive)
		var syntheticMsg string
		if body.Action == "retry" {
			// User wants to redo the current step (full retry via state-machine self-loop).
			syntheticMsg = fmt.Sprintf("Step %s completed but user wants to retry it. Please re-run step %s from scratch.", session.CurrentStepID, session.CurrentStepID)
		} else {
			// Default: user confirmed, proceed to next step.
			syntheticMsg = fmt.Sprintf("Step %s completed. User confirmed. Please proceed.", session.CurrentStepID)
		}
		go triggerNextChatTurn(
			session.ConversationID, sessionID, session.PluginID,
			session.CurrentStepID, userID, syntheticMsg,
		)
		common.ReplyOK(w, map[string]any{"action": body.Action, "message": syntheticMsg})

	default:
		common.ReplyErr(w, fmt.Sprintf("step status %q is not resumable", step.Status), http.StatusConflict)
	}
}
