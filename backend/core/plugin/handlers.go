package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

// resolveValuePaths normalises a human-uploaded value by ensuring it carries a stable
// absolute path when the value contains a local file path.
// Signed URL generation is intentionally NOT done here — signed URLs expire and must
// be generated fresh on every API response (see signArtifactImagePath called from
// enrichSlots and GetSlotItemVersionsByIndex).
// Values that are not JSON objects with a path field are returned unchanged.
func resolveValuePaths(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	pathVal, ok := m["path"].(string)
	if !ok || pathVal == "" {
		return raw
	}
	// Strip any pre-existing url field so callers always re-sign on read.
	delete(m, "url")
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// signArtifactImagePath enriches an artifact value with a signed URL when it contains
// a local file path. Works for both AI-generated artifacts and human-uploaded snapshots.
// External http(s) URLs stored in the path field are moved to the url field for consistent
// frontend handling. Local paths are signed fresh (avoiding stale signed URLs in the DB).
// The path field is preserved alongside url so the algorithm layer can still read the file.
// Values without a path field, or that already have a url field, are returned unchanged.
// The contentType parameter is used only to skip non-image processing; pass "image" when
// the content type is known, or pass "" to attempt signing for any path-bearing value.
func signArtifactImagePath(raw json.RawMessage, contentType string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	if contentType != "" && contentType != "image" {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	pathVal, _ := m["path"].(string)
	if pathVal == "" {
		return raw
	}
	// Always re-sign regardless of existing url — stored urls may have expired.
	// External or inline URL stored in path field — move it to url for consistent frontend handling.
	if strings.HasPrefix(pathVal, "http://") || strings.HasPrefix(pathVal, "https://") ||
		strings.HasPrefix(pathVal, "data:") {
		m["url"] = pathVal
		delete(m, "path")
		out, err := json.Marshal(m)
		if err != nil {
			return raw
		}
		return out
	}
	// Local path: generate signed URL and keep path for algorithm access.
	signed := doc.StaticFileURLFromFullPath(pathVal)
	if signed == "" {
		return raw
	}
	m["url"] = signed
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

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
	SortOrder     *int            `json:"sort_order,omitempty"`
	Selected      bool            `json:"selected"`
	ArtifactKey   string          `json:"artifact_key"`
	CreatedAt     time.Time       `json:"created_at"`
	ContentType   string          `json:"content_type,omitempty"`
	ArtifactValue json.RawMessage `json:"artifact_value,omitempty"`
	Caption       *string         `json:"caption,omitempty"`
	ChangeSource  string          `json:"change_source,omitempty"`
	RevisionCount int             `json:"revision_count,omitempty"`
	OrderVersion  *int            `json:"order_version,omitempty"`

	// Internal fields — used by enrichSlots, never serialised to the client.
	ArtifactSeq     *int            `json:"-"`
	HumanArtifactID *string         `json:"-"`
	StepID          string          `json:"-"`
	Attempt         int             `json:"-"`
	ContentSnapshot json.RawMessage `json:"-"`
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
		SlotID:          r.SlotID,
		Revision:        r.Revision,
		ListIndex:       r.ListIndex,
		Selected:        r.Selected,
		ArtifactKey:     r.ArtifactKey,
		ArtifactSeq:     r.ArtifactSeq,
		HumanArtifactID: r.HumanArtifactID,
		StepID:          r.StepID,
		Attempt:         r.Attempt,
		CreatedAt:       r.CreatedAt,
		ChangeSource:    r.ChangeSource,
		ContentSnapshot: r.ContentSnapshot,
	}
}

// enrichSlots fills ContentType, ArtifactValue, Caption, RevisionCount, SortOrder,
// and OrderVersion on each slotDTO by querying sub_agent_artifacts, plugin_slot_revisions,
// and plugin_slot_order.
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
			Where("task_id IN ? AND hidden = ?", ids, false).
			Order("task_id ASC, artifact_key ASC, seq ASC").
			Find(&arts)
		for _, a := range arts {
			k := a.TaskID + "#" + a.ArtifactKey
			artifactsByTask[k] = append(artifactsByTask[k], a)
		}
	}

	// Step 3: load revision counts per (session_id, slot_id, list_index).
	type revKey struct {
		slotID    string
		listIndex *int
	}
	revCounts := map[string]int{}
	type revCountRow struct {
		SlotID    string `gorm:"column:slot_id"`
		ListIndex *int   `gorm:"column:list_index"`
		Count     int    `gorm:"column:cnt"`
	}
	var rcRows []revCountRow
	db.WithContext(ctx).Raw(
		`SELECT slot_id, list_index, COUNT(*) AS cnt FROM plugin_slot_revisions
		 WHERE session_id = ? GROUP BY slot_id, list_index`,
		sessionID,
	).Scan(&rcRows)
	for _, rc := range rcRows {
		key := rc.SlotID + "|"
		if rc.ListIndex != nil {
			key += fmt.Sprintf("%d", *rc.ListIndex)
		}
		revCounts[key] = rc.Count
	}

	// Step 4: load slot order info for order_version and sort_order lookup.
	orderBySlot := map[string]*orm.PluginSlotOrder{}
	var orders []orm.PluginSlotOrder
	db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&orders)
	for i := range orders {
		orderBySlot[orders[i].SlotID] = &orders[i]
	}

	// Step 5: assign values to each slotDTO
	for i := range slots {
		slot := &slots[i]

		// Unified value resolution (priority order):
		//   1. HumanArtifactID != nil → human revision: read from plugin_human_artifacts.
		//   2. ArtifactSeq != nil     → AI revision: read from sub_agent_artifacts by seq.
		//   3. ContentSnapshot        → legacy fallback (pre-migration rows).
		var resolved json.RawMessage
		var resolvedContentType string
		var resolvedCaption *string

		if slot.HumanArtifactID != nil {
			var ha orm.PluginHumanArtifact
			haErr := db.WithContext(ctx).Where("id = ?", *slot.HumanArtifactID).First(&ha).Error
			if haErr == nil {
				resolvedContentType = resolveContentType(ha.ContentType, ha.Value)
				resolved = signArtifactImagePath(ha.Value, resolvedContentType)
				resolvedCaption = ha.Caption
			} else {
				fmt.Printf("[enrichSlots] WARN: HumanArtifactID=%s not found for slot_id=%s list_index=%v: %v\n",
					*slot.HumanArtifactID, slot.SlotID, slot.ListIndex, haErr)
			}
		} else if slot.ArtifactSeq != nil {
			tid := taskIDByStep[stepKey{slot.StepID, slot.Attempt}]
			if tid == "" {
				fmt.Printf("[enrichSlots] WARN: no task_id for step_id=%s attempt=%d slot_id=%s\n",
					slot.StepID, slot.Attempt, slot.SlotID)
			} else {
				k := tid + "#" + slot.ArtifactKey
				for j := range artifactsByTask[k] {
					if artifactsByTask[k][j].Seq == *slot.ArtifactSeq {
						a := &artifactsByTask[k][j]
						resolvedContentType = resolveContentType(a.ContentType, a.Value)
						resolved = signArtifactImagePath(a.Value, resolvedContentType)
						resolvedCaption = a.Caption
						break
					}
				}
				if resolved == nil {
					fmt.Printf("[enrichSlots] WARN: ArtifactSeq=%d not found in task=%s key=%s slot_id=%s\n",
						*slot.ArtifactSeq, tid, slot.ArtifactKey, slot.SlotID)
				}
			}
		} else {
			fmt.Printf("[enrichSlots] INFO: slot_id=%s list_index=%v revision=%d has no HumanArtifactID and no ArtifactSeq, ContentSnapshot len=%d\n",
				slot.SlotID, slot.ListIndex, slot.Revision, len(slot.ContentSnapshot))
		}

		// Legacy fallback: ContentSnapshot for pre-migration rows.
		if resolved == nil && len(slot.ContentSnapshot) > 0 {
			resolved = signArtifactImagePath(slot.ContentSnapshot, "")
		}

		if resolved == nil {
			fmt.Printf("[enrichSlots] WARN: resolved=nil for slot_id=%s list_index=%v revision=%d change_source=%s HumanArtifactID=%v ArtifactSeq=%v\n",
				slot.SlotID, slot.ListIndex, slot.Revision, slot.ChangeSource, slot.HumanArtifactID, slot.ArtifactSeq)
		}

		if resolved != nil {
			slot.ArtifactValue = resolved
			if resolvedContentType != "" {
				slot.ContentType = resolvedContentType
			}
			slot.Caption = resolvedCaption
		}

		// Revision count.
		rcKey := slot.SlotID + "|"
		if slot.ListIndex != nil {
			rcKey += fmt.Sprintf("%d", *slot.ListIndex)
		}
		slot.RevisionCount = revCounts[rcKey]

		// sort_order and order_version from plugin_slot_order.
		// single slots (list_index IS NULL) get sort_order=0 as a stable sentinel.
		if slot.ListIndex == nil {
			so := 0
			slot.SortOrder = &so
		} else if ord, ok := orderBySlot[slot.SlotID]; ok {
			var list []int
			_ = json.Unmarshal(ord.OrderList, &list)
			for pos, li := range list {
				if li == *slot.ListIndex {
					so := pos + 1
					slot.SortOrder = &so
					break
				}
			}
			ov := ord.OrderVersion
			slot.OrderVersion = &ov
		}
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

// ReorderSlotItems handles PATCH /plugin-sessions/{session_id}/slots/{slot_id}/order.
// Body: {"order": [1,0,2], "version": N}
// order is the desired new sequence expressed as list_index values.
func ReorderSlotItems(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	if sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id and slot_id required", http.StatusBadRequest)
		return
	}
	var body struct {
		Order   []int `json:"order"`
		Version int   `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Order) == 0 {
		common.ReplyErr(w, "invalid body: order required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()

	if err := ReorderSlot(ctx, db, sessionID, slotID, body.Order, body.Version); err != nil {
		if err == ErrConflict {
			common.ReplyErr(w, "version conflict", http.StatusConflict)
			return
		}
		common.ReplyErr(w, "reorder failed", http.StatusInternalServerError)
		return
	}
	// Return updated order_version.
	updated, _ := GetSlotOrder(ctx, db, sessionID, slotID)
	newVersion := body.Version + 1
	if updated != nil {
		newVersion = updated.OrderVersion
	}
	common.ReplyOK(w, map[string]any{"order_version": newVersion})
}

// GetSlotOrderHandler handles GET /plugin-sessions/{session_id}/slots/{slot_id}/order.
// Returns the order_list and order_version for a slot, used by Python save_artifact
// to translate sort_order → list_index without exposing list_index to the AI.
func GetSlotOrderHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	if sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id and slot_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	row, err := GetSlotOrder(r.Context(), db, sessionID, slotID)
	if err != nil {
		common.ReplyErr(w, "query order failed", http.StatusInternalServerError)
		return
	}
	if row == nil {
		common.ReplyOK(w, map[string]any{
			"order_list":    []int{},
			"order_version": 0,
		})
		return
	}
	var list []int
	_ = json.Unmarshal(row.OrderList, &list)
	common.ReplyOK(w, map[string]any{
		"order_list":    list,
		"order_version": row.OrderVersion,
	})
}

// CreateSlotItem handles POST /plugin-sessions/{session_id}/slots/{slot_id}/items.
// Appends a new human-created item to a list slot or inserts before a given sort_order.
// Body: { value: {...}, caption?: string, insert_before?: number }
func CreateSlotItem(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	if sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id and slot_id required", http.StatusBadRequest)
		return
	}
	var body struct {
		Value        json.RawMessage `json:"value"`
		Caption      *string         `json:"caption,omitempty"`
		InsertBefore *int            `json:"insert_before,omitempty"`
		ContentType  string          `json:"content_type,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Value) == 0 {
		common.ReplyErr(w, "invalid body: value required", http.StatusBadRequest)
		return
	}
	if body.ContentType == "" {
		body.ContentType = "text"
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	// Get an existing selected revision to borrow its artifact_key and step info.
	var anyRev orm.PluginSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true).
		First(&anyRev).Error; err != nil {
		common.ReplyErr(w, "slot has no existing items; cannot infer artifact_key", http.StatusBadRequest)
		return
	}
	// Write new list revision via WriteSlotRevisionWithHumanArtifact so that
	// content_type is persisted correctly (required for image rendering).
	newRev, err := WriteSlotRevisionWithHumanArtifact(ctx, db,
		sessionID, slotID, anyRev.ArtifactKey, anyRev.StepID, anyRev.Attempt,
		"list", nil,
		body.ContentType, resolveValuePaths(body.Value), body.Caption,
	)
	if err != nil {
		common.ReplyErr(w, "create item failed", http.StatusInternalServerError)
		return
	}
	// If insert_before is specified, reorder so the new item sits at that position.
	if body.InsertBefore != nil && newRev.ListIndex != nil {
		if orderRow, err := GetSlotOrder(ctx, db, sessionID, slotID); err == nil && orderRow != nil {
			var currentOrder []int
			_ = json.Unmarshal(orderRow.OrderList, &currentOrder)
			newIdx := *newRev.ListIndex
			target := *body.InsertBefore - 1
			if target >= 0 && target < len(currentOrder) {
				reordered := make([]int, 0, len(currentOrder))
				for _, v := range currentOrder {
					if v != newIdx {
						reordered = append(reordered, v)
					}
				}
				final := append(append(reordered[:target:target], newIdx), reordered[target:]...)
				_ = ReorderSlot(ctx, db, sessionID, slotID, final, orderRow.OrderVersion)
			}
		}
	}
	// Persist caption if provided.
	if body.Caption != nil {
		var step orm.PluginSessionStep
		if err := db.WithContext(ctx).
			Where("session_id = ? AND step_id = ? AND attempt = ?", sessionID, anyRev.StepID, anyRev.Attempt).
			First(&step).Error; err == nil {
			cap := *body.Caption
			db.WithContext(ctx).Model(&orm.SubAgentArtifact{}).
				Where("task_id = ? AND artifact_key = ?", step.TaskID, anyRev.ArtifactKey).
				Update("caption", &cap)
		}
	}
	common.ReplyOK(w, map[string]any{
		"type":       "slot_item_created",
		"session_id": sessionID,
		"slot_id":    slotID,
		"revision":   newRev.Revision,
	})
}

// SaveArtifactByKey handles POST /plugin-sessions/{session_id}/artifacts.
// Allows ChatAgent to write a plugin artifact directly by artifact_key without
// going through a SubAgent task. Looks up the slot binding via the Python API,
// then writes a new AI slot revision for the given artifact_key.
// Body: { artifact_key: string, value: {...}, content_type?: string,
//
//	sort_order?: int, caption?: string, step_id?: string }
func SaveArtifactByKey(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body struct {
		ArtifactKey string          `json:"artifact_key"`
		Value       json.RawMessage `json:"value"`
		ContentType string          `json:"content_type,omitempty"`
		SortOrder   *int            `json:"sort_order,omitempty"`
		Caption     *string         `json:"caption,omitempty"`
		StepID      string          `json:"step_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ArtifactKey == "" {
		common.ReplyErr(w, "invalid body: artifact_key and value required", http.StatusBadRequest)
		return
	}
	if len(body.Value) == 0 {
		common.ReplyErr(w, "value must not be empty", http.StatusBadRequest)
		return
	}
	if body.ContentType == "" {
		body.ContentType = "text"
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()

	// Resolve plugin_id from session.
	var sess orm.PluginSession
	if err := db.WithContext(ctx).Where("id = ?", sessionID).First(&sess).Error; err != nil {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}

	// Resolve slot binding for the artifact_key via Python plugin API.
	slotID, cardinality := resolveSlotBinding(sess.PluginID, body.ArtifactKey)
	if slotID == "" {
		common.ReplyErr(w, fmt.Sprintf("no slot binding for artifact_key %q in plugin %q", body.ArtifactKey, sess.PluginID), http.StatusBadRequest)
		return
	}

	// Determine which step_id to attribute this artifact to.
	stepID := body.StepID
	attempt := 1
	if stepID == "" {
		stepID = sess.CurrentStepID
	}
	if latestStep, _ := GetLatestStep(ctx, db, sessionID, stepID); latestStep != nil {
		attempt = latestStep.Attempt
	}

	// Resolve list_index from sort_order when provided.
	var listIndex *int
	if cardinality == "list" && body.SortOrder != nil {
		order, _ := GetSlotOrder(ctx, db, sessionID, slotID)
		if order != nil {
			var orderList []int
			_ = json.Unmarshal(order.OrderList, &orderList)
			idx := *body.SortOrder - 1
			if idx >= 0 && idx < len(orderList) {
				li := orderList[idx]
				listIndex = &li
			}
		}
	}

	rev, err := WriteSlotRevisionWithHumanArtifact(ctx, db,
		sessionID, slotID, body.ArtifactKey, stepID, attempt, cardinality, listIndex,
		body.ContentType, body.Value, body.Caption)
	if err != nil {
		common.ReplyErr(w, "write slot revision failed", http.StatusInternalServerError)
		return
	}
	common.ReplyJSON(w, map[string]any{
		"status":       "ok",
		"session_id":   sessionID,
		"slot_id":      slotID,
		"artifact_key": body.ArtifactKey,
		"revision":     rev.Revision,
	})
}
