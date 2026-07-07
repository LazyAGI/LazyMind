package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
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
// a local file path. Delegates to subagent.SignArtifactImageValue.
func signArtifactImagePath(raw json.RawMessage, contentType string) json.RawMessage {
	return subagent.SignArtifactImageValue(contentType, raw)
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
	Slot          string          `json:"slot"`
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

// buildStepIntentMap loads all step intents for a session and returns a map[step_id]intent_context.
// Returns an empty map on error (non-fatal).
func buildStepIntentMap(ctx context.Context, db *gorm.DB, sessionID string) map[string]string {
	intents, err := ListStepIntents(ctx, db, sessionID)
	m := make(map[string]string, len(intents))
	if err != nil {
		return m
	}
	for _, si := range intents {
		if si.IntentContext != "" && si.IntentContext != "{}" {
			m[si.StepID] = si.IntentContext
		}
	}
	return m
}

func toSlotDTO(r *orm.PluginSlotRevision) slotDTO {
	return slotDTO{
		SlotID:          r.SlotID,
		Revision:        r.Revision,
		ListIndex:       r.ListIndex,
		Selected:        r.Selected,
		Slot:            r.Slot,
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
// sub_agent_artifacts(task_id, slot) ordered by seq ASC and pick the
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
			Order("task_id ASC, slot ASC, seq ASC").
			Find(&arts)
		for _, a := range arts {
			k := a.TaskID + "#" + a.Slot
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
				k := tid + "#" + slot.Slot
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
					fmt.Printf("[enrichSlots] WARN: ArtifactSeq=%d not found in task=%s slot=%s slot_id=%s\n",
						*slot.ArtifactSeq, tid, slot.Slot, slot.SlotID)
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
	if s.Dismissed {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
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
	if s.Dismissed {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	revisions, err := LoadSelectedSlots(ctx, db, sessionID)
	if err != nil {
		common.ReplyErr(w, "query slots failed", http.StatusInternalServerError)
		return
	}
	out := make([]slotDTO, 0, len(revisions))
	for i := range revisions {
		out = append(out, toSlotDTO(&revisions[i]))
	}
	enrichSlots(ctx, db, sessionID, out)
	common.ReplyOK(w, map[string]any{"slots": out})
}

// GetSessionSteps handles GET /plugin-sessions/{session_id}/steps.
// Returns all step execution records for the session, ordered by created_at ASC.
// The frontend uses this in completed state to render the rollback step selector.
func GetSessionSteps(w http.ResponseWriter, r *http.Request) {
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
	sess, err := GetSession(ctx, db, sessionID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	if sess.Dismissed {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	steps, err := ListSteps(ctx, db, sessionID)
	if err != nil {
		common.ReplyErr(w, "query steps failed", http.StatusInternalServerError)
		return
	}
	type stepDTO struct {
		ID            string `json:"id"`
		SessionID     string `json:"session_id"`
		StepID        string `json:"step_id"`
		Attempt       int    `json:"attempt"`
		TaskID        string `json:"task_id"`
		Status        string `json:"status"`
		IntentContext string `json:"intent_context,omitempty"`
		CreatedAt     string `json:"created_at"`
		UpdatedAt     string `json:"updated_at"`
	}
	intentMap := buildStepIntentMap(ctx, db, sessionID)
	out := make([]stepDTO, 0, len(steps))
	for _, s := range steps {
		out = append(out, stepDTO{
			ID:            s.ID,
			SessionID:     s.SessionID,
			StepID:        s.StepID,
			Attempt:       s.Attempt,
			TaskID:        s.TaskID,
			Status:        s.Status,
			IntentContext: intentMap[s.StepID],
			CreatedAt:     s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			UpdatedAt:     s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	common.ReplyOK(w, map[string]any{"steps": out})
}

// PatchSessionSlot handles PATCH /plugin-sessions/{session_id}/slots/{slot_id}.
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
	s, err := GetSession(ctx, db, sessionID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	if s.Dismissed {
		common.ReplyErr(w, "session is dismissed", http.StatusConflict)
		return
	}
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

// artifactSummaryItem is one entry in the per-step artifact summary list.
type artifactSummaryItem struct {
	Slot        string `json:"slot"`
	ContentType string `json:"content_type"`
	Preview     string `json:"preview"` // text snippet (≤30 chars) or filename
}

// stepAttemptDTO represents one execution attempt of a step.
type stepAttemptDTO struct {
	Attempt       int     `json:"attempt"`
	Status        string  `json:"status"`
	DurationSec   float64 `json:"duration_sec"`   // -1 if not finished
	ArtifactCount int     `json:"artifact_count"` // slot-revision count for this attempt
	StartedAt     string  `json:"started_at"`
}

// stateGraphNodeDTO is one node in the StateGraph response.
type stateGraphNodeDTO struct {
	ID            string                `json:"id"`
	Label         string                `json:"label"`
	StepIndex     int                   `json:"step_index"` // 1-based; 0 for terminal nodes
	Status        string                `json:"status"`
	IsCurrent     bool                  `json:"is_current"`
	ArtifactItems []artifactSummaryItem `json:"artifact_items"` // latest-attempt artifacts
	StepAttempts  []stepAttemptDTO      `json:"step_attempts"`
}

// stateGraphEdgeDTO is one directed edge in the StateGraph response.
type stateGraphEdgeDTO struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition"`
	// EdgeType: "executed" | "current_direct" | "current_reachable" | "skipped"
	// Computed server-side from execution history and current step.
	EdgeType string `json:"edge_type"`
}

// stateGraphResponse is the full response for GET /plugin-sessions/{session_id}/state-graph.
type stateGraphResponse struct {
	Nodes         []stateGraphNodeDTO `json:"nodes"`
	Edges         []stateGraphEdgeDTO `json:"edges"`
	Initial       string              `json:"initial"`
	CurrentStepID string              `json:"current_step_id"`
}

// pluginStateTransitionEdge matches one entry in state.transitions[from][].
type pluginStateTransitionEdge struct {
	To        string `json:"to"`
	Condition string `json:"condition"`
}

// pluginStateSpec is the relevant subset of the Python /api/plugins/{id} response.
// state.steps is a map[step_id]→{label,...}; state.transitions is map[from]→[]{to, condition}.
type pluginStateSpec struct {
	State struct {
		Initial     string                                 `json:"initial"`
		Steps       map[string]map[string]any              `json:"steps"`
		Transitions map[string][]pluginStateTransitionEdge `json:"transitions"`
	} `json:"state"`
}

// buildArtifactPreview extracts a human-readable preview from a raw artifact value JSON.
// text: first 30 runes of the "text" field.
// image: filename (with extension) from "path" or "url"; middle-truncated to 30 chars.
// other content types: empty string.
func buildArtifactPreview(contentType string, raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	switch contentType {
	case "text":
		text, _ := m["text"].(string)
		runes := []rune(text)
		if len(runes) > 30 {
			return string(runes[:30]) + "…"
		}
		return text
	case "image":
		pathVal, _ := m["path"].(string)
		if pathVal == "" {
			pathVal, _ = m["url"].(string)
		}
		if pathVal == "" {
			return ""
		}
		// Extract filename from path.
		parts := strings.Split(strings.ReplaceAll(pathVal, "\\", "/"), "/")
		name := parts[len(parts)-1]
		// Strip query params.
		if idx := strings.Index(name, "?"); idx >= 0 {
			name = name[:idx]
		}
		// Middle-truncate to 30 chars: keep first N and last M chars.
		runes := []rune(name)
		if len(runes) > 30 {
			return string(runes[:13]) + "…" + string(runes[len(runes)-14:])
		}
		return name
	default:
		return ""
	}
}

// GetStateGraph handles GET /plugin-sessions/{session_id}/state-graph.
// Combines the plugin state machine topology from Python with live step statuses from DB.
func GetStateGraph(w http.ResponseWriter, r *http.Request) {
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

	// 1. Load plugin session.
	sess, err := GetSession(ctx, db, sessionID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	if sess.Dismissed {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}

	// 2. Fetch plugin spec from Python to get state machine topology.
	upstream := common.ChatServiceEndpoint() + "/api/plugins/" + sess.PluginID
	upCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(upCtx, http.MethodGet, upstream, nil)
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
	var spec pluginStateSpec
	if decErr := json.NewDecoder(resp.Body).Decode(&spec); decErr != nil {
		common.ReplyErr(w, "decode plugin spec failed", http.StatusInternalServerError)
		return
	}

	// 3. Query all step attempts with timing + artifact count.
	type stepRow struct {
		StepID        string    `gorm:"column:step_id"`
		Attempt       int       `gorm:"column:attempt"`
		Status        string    `gorm:"column:status"`
		TaskID        string    `gorm:"column:task_id"`
		CreatedAt     time.Time `gorm:"column:created_at"`
		UpdatedAt     time.Time `gorm:"column:updated_at"`
		ArtifactCount int       `gorm:"column:artifact_count"`
	}
	var stepRows []stepRow
	if stepQueryErr := db.WithContext(ctx).Raw(`
		SELECT
			s.step_id,
			s.attempt,
			s.status,
			s.task_id,
			s.created_at,
			s.updated_at,
			COALESCE(a.artifact_count, 0) AS artifact_count
		FROM plugin_session_steps s
		LEFT JOIN (
			SELECT step_id, attempt, COUNT(*) AS artifact_count
			FROM plugin_slot_revisions
			WHERE session_id = ?
			GROUP BY step_id, attempt
		) a ON a.step_id = s.step_id AND a.attempt = s.attempt
		WHERE s.session_id = ?
		ORDER BY s.step_id, s.attempt ASC
	`, sessionID, sessionID).Scan(&stepRows).Error; stepQueryErr != nil {
		common.ReplyErr(w, "query step rows failed", http.StatusInternalServerError)
		return
	}
	type stepInfo struct {
		latestStatus  string
		latestAttempt int
		attempts      []stepAttemptDTO
	}
	stepMap := make(map[string]*stepInfo)
	for _, r := range stepRows {
		si, ok := stepMap[r.StepID]
		if !ok {
			si = &stepInfo{}
			stepMap[r.StepID] = si
		}
		// Use updated_at - created_at as duration for completed steps.
		dur := -1.0
		terminalStatuses := map[string]bool{"succeeded": true, "failed": true, "interrupted": true, "canceled": true}
		if terminalStatuses[r.Status] {
			dur = r.UpdatedAt.Sub(r.CreatedAt).Seconds()
		}
		si.attempts = append(si.attempts, stepAttemptDTO{
			Attempt:       r.Attempt,
			Status:        r.Status,
			DurationSec:   dur,
			ArtifactCount: r.ArtifactCount,
			StartedAt:     r.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
		})
		if r.Attempt >= si.latestAttempt {
			si.latestAttempt = r.Attempt
			si.latestStatus = r.Status
		}
	}

	// 4. Query artifacts for the latest attempt of each step.
	//    For text artifacts: take first 30 runes of the "text" field.
	//    For image artifacts: take the filename from the "path" or "url" field.
	//    De-duplicate by slot, keeping the row with the highest seq.
	type artifactRow struct {
		StepID      string `gorm:"column:step_id"`
		Attempt     int    `gorm:"column:attempt"`
		Slot        string `gorm:"column:slot"`
		ContentType string `gorm:"column:content_type"`
		Value       []byte `gorm:"column:value"`
	}
	var artifactRows []artifactRow
	if artifactQueryErr := db.WithContext(ctx).Raw(`
		SELECT a.slot, a.content_type, a.value, s.step_id, s.attempt
		FROM sub_agent_artifacts a
		JOIN plugin_session_steps s ON s.task_id = a.task_id
		WHERE s.session_id = ?
		  AND a.hidden = false
		  AND a.seq = (
			SELECT MAX(a2.seq)
			FROM sub_agent_artifacts a2
			WHERE a2.task_id = a.task_id
			  AND a2.slot = a.slot
		  )
		  AND s.attempt = (
			SELECT MAX(s2.attempt)
			FROM plugin_session_steps s2
			WHERE s2.session_id = s.session_id
			  AND s2.step_id = s.step_id
		  )
		ORDER BY s.step_id, a.slot
	`, sessionID).Scan(&artifactRows).Error; artifactQueryErr != nil {
		common.ReplyErr(w, "query artifact rows failed", http.StatusInternalServerError)
		return
	}

	// Group artifact items by step_id, de-dup by slot.
	stepArtifacts := make(map[string][]artifactSummaryItem)
	seen := make(map[string]bool) // "step_id:slot"
	for _, r := range artifactRows {
		k := r.StepID + ":" + r.Slot
		if seen[k] {
			continue
		}
		seen[k] = true
		preview := buildArtifactPreview(r.ContentType, r.Value)
		stepArtifacts[r.StepID] = append(stepArtifacts[r.StepID], artifactSummaryItem{
			Slot:        r.Slot,
			ContentType: r.ContentType,
			Preview:     preview,
		})
	}

	// 5. Build nodes — __start__ + all declared steps (in transition order) + __end__.
	// Use BFS from initial to enumerate steps in topological order.
	startNode := stateGraphNodeDTO{
		ID:        "__start__",
		Label:     "__start__",
		Status:    "succeeded",
		IsCurrent: false,
	}

	endStatus := "pending"
	if sess.Status == "completed" {
		endStatus = "succeeded"
	}
	endNode := stateGraphNodeDTO{
		ID:        "__end__",
		Label:     "__end__",
		Status:    endStatus,
		IsCurrent: false,
	}

	// BFS to produce a stable step ordering from the state machine topology.
	visited := map[string]bool{"__start__": true, "__end__": true}
	queue := []string{"__start__"}
	orderedStepIDs := []string{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, edge := range spec.State.Transitions[cur] {
			if !visited[edge.To] && edge.To != "__end__" {
				visited[edge.To] = true
				orderedStepIDs = append(orderedStepIDs, edge.To)
				queue = append(queue, edge.To)
			}
		}
	}

	nodes := []stateGraphNodeDTO{startNode}
	for i, stepID := range orderedStepIDs {
		stepData, hasStep := spec.State.Steps[stepID]
		label := stepID
		if hasStep {
			if lv, ok := stepData["label"].(string); ok && lv != "" {
				label = lv
			}
		}
		status := "pending"
		var attempts []stepAttemptDTO
		if si, ok := stepMap[stepID]; ok {
			status = si.latestStatus
			attempts = si.attempts
		}
		nodes = append(nodes, stateGraphNodeDTO{
			ID:            stepID,
			Label:         label,
			StepIndex:     i + 1,
			Status:        status,
			IsCurrent:     stepID == sess.CurrentStepID,
			StepAttempts:  attempts,
			ArtifactItems: stepArtifacts[stepID],
		})
	}
	nodes = append(nodes, endNode)

	// 6. Build edges with edge_type based on execution history.
	//
	// edge_type rules:
	//   "executed"         — both from and to have been executed (status != pending)
	//   "current_direct"   — from == current step (direct successor)
	//   "current_reachable"— reachable from current via BFS (not direct)
	//   "skipped"          — neither executed nor reachable from current
	//
	// Treat __start__ as always executed; __end__ as executed when session is completed.
	executedNodes := map[string]bool{"__start__": true}
	if sess.Status == "completed" || sess.Status == "failed" {
		executedNodes["__end__"] = true
	}
	for nodeID, si := range stepMap {
		if si.latestStatus != "" && si.latestStatus != "pending" {
			executedNodes[nodeID] = true
		}
	}

	// BFS from current step to find all reachable nodes (direct + indirect).
	directSuccessors := map[string]bool{}
	reachableFromCurrent := map[string]bool{}
	if sess.CurrentStepID != "" {
		for _, e := range spec.State.Transitions[sess.CurrentStepID] {
			directSuccessors[e.To] = true
			reachableFromCurrent[e.To] = true
		}
		bfsQueue := []string{}
		for id := range directSuccessors {
			bfsQueue = append(bfsQueue, id)
		}
		bfsVisited := map[string]bool{sess.CurrentStepID: true}
		for _, id := range bfsQueue {
			bfsVisited[id] = true
		}
		for len(bfsQueue) > 0 {
			cur2 := bfsQueue[0]
			bfsQueue = bfsQueue[1:]
			for _, e := range spec.State.Transitions[cur2] {
				if !bfsVisited[e.To] {
					bfsVisited[e.To] = true
					reachableFromCurrent[e.To] = true
					bfsQueue = append(bfsQueue, e.To)
				}
			}
		}
	}

	edges := make([]stateGraphEdgeDTO, 0)
	for fromID, edgeList := range spec.State.Transitions {
		for _, edge := range edgeList {
			// Skip self-loops.
			if fromID == edge.To {
				continue
			}
			var edgeType string
			switch {
			case executedNodes[fromID] && executedNodes[edge.To]:
				edgeType = "executed"
			case fromID == sess.CurrentStepID && directSuccessors[edge.To]:
				edgeType = "current_direct"
			case reachableFromCurrent[edge.To] && !executedNodes[edge.To]:
				edgeType = "current_reachable"
			default:
				edgeType = "skipped"
			}
			edges = append(edges, stateGraphEdgeDTO{
				From:      fromID,
				To:        edge.To,
				Condition: edge.Condition,
				EdgeType:  edgeType,
			})
		}
	}

	// 7. Determine initial node id.
	initial := spec.State.Initial
	if initial == "" {
		initial = "__start__"
	}

	out := stateGraphResponse{
		Nodes:         nodes,
		Edges:         edges,
		Initial:       initial,
		CurrentStepID: sess.CurrentStepID,
	}
	common.ReplyOK(w, out)
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

// SyncSessionSearchConfig handles POST /plugin-sessions/{session_id}:sync-search-config.
// Persists the current UI knowledge-base selection onto the parent conversation so
// analyze_subject KB prefetch can read filters.kb_id.
// Body: {"search_config": {"dataset_list": [{"id": "..."}], "creators": [], "tags": []}}
func SyncSessionSearchConfig(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body struct {
		SearchConfig map[string]any `json:"search_config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.SearchConfig) == 0 {
		common.ReplyErr(w, "search_config required", http.StatusBadRequest)
		return
	}
	db := store.DB()
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
	userID := store.UserID(r)
	if err := persistConversationSearchConfig(db, session.ConversationID, userID, body.SearchConfig); err != nil {
		common.ReplyErr(w, "persist search_config failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"conversation_id": session.ConversationID})
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
	reorderSess, err := GetSession(ctx, db, sessionID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	if reorderSess.Dismissed {
		common.ReplyErr(w, "session is dismissed", http.StatusConflict)
		return
	}

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
	ctx := r.Context()
	slotOrderSess, err := GetSession(ctx, db, sessionID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	if slotOrderSess.Dismissed {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	row, err := GetSlotOrder(ctx, db, sessionID, slotID)
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
	createItemSess, siErr := GetSession(ctx, db, sessionID)
	if siErr != nil {
		if IsNotFound(siErr) {
			common.ReplyErr(w, "session not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
		return
	}
	if createItemSess.Dismissed {
		common.ReplyErr(w, "session is dismissed", http.StatusConflict)
		return
	}
	// Get an existing selected revision to borrow its slot and step info.
	var anyRev orm.PluginSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true).
		First(&anyRev).Error; err != nil {
		common.ReplyErr(w, "slot has no existing items; cannot infer slot", http.StatusBadRequest)
		return
	}
	// Write new list revision via WriteSlotRevisionWithHumanArtifact so that
	// content_type is persisted correctly (required for image rendering).
	newRev, err := WriteSlotRevisionWithHumanArtifact(ctx, db,
		sessionID, slotID, anyRev.Slot, anyRev.StepID, anyRev.Attempt,
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
				Where("task_id = ? AND slot = ?", step.TaskID, anyRev.Slot).
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
// Allows ChatAgent to write a plugin artifact directly by slot without
// going through a SubAgent task. Looks up the slot binding via the Python API,
// then writes a new AI slot revision for the given slot.
// Body: { slot: string, value: {...}, content_type?: string,
//
//	sort_order?: int, caption?: string, step_id?: string }
func SaveArtifactByKey(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body struct {
		Slot        string          `json:"slot"`
		Value       json.RawMessage `json:"value"`
		ContentType string          `json:"content_type,omitempty"`
		SortOrder   *int            `json:"sort_order,omitempty"`
		Caption     *string         `json:"caption,omitempty"`
		StepID      string          `json:"step_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Slot == "" {
		common.ReplyErr(w, "invalid body: slot and value required", http.StatusBadRequest)
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
	if sess.Dismissed {
		common.ReplyErr(w, "session is dismissed", http.StatusConflict)
		return
	}

	// Resolve slot binding for the slot via Python plugin API.
	slotID, cardinality := resolveSlotBinding(sess.PluginID, body.Slot)
	if slotID == "" {
		common.ReplyErr(w, fmt.Sprintf("no slot binding for slot %q in plugin %q", body.Slot, sess.PluginID), http.StatusBadRequest)
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
		sessionID, slotID, body.Slot, stepID, attempt, cardinality, listIndex,
		body.ContentType, body.Value, body.Caption)
	if err != nil {
		common.ReplyErr(w, "write slot revision failed", http.StatusInternalServerError)
		return
	}
	common.ReplyJSON(w, map[string]any{
		"status":     "ok",
		"session_id": sessionID,
		"slot_id":    slotID,
		"slot":       body.Slot,
		"revision":   rev.Revision,
	})
}

// DismissSessionHandler handles POST /plugin-sessions/{session_id}:dismiss.
// Marks the session as dismissed, hiding it from all active-session lookups.
func DismissSessionHandler(w http.ResponseWriter, r *http.Request) {
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
	if err := DismissSession(r.Context(), db, sessionID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || err.Error() == "session not found or already dismissed" {
			common.ReplyErr(w, "session not found or already dismissed", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "dismiss failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"session_id": sessionID, "dismissed": true})
}

// RestoreSessionHandler handles POST /plugin-sessions/{session_id}:restore.
// Un-dismisses a previously dismissed session, subject to no active/waiting session existing.
func RestoreSessionHandler(w http.ResponseWriter, r *http.Request) {
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
	if err := RestoreSession(r.Context(), db, sessionID); err != nil {
		msg := err.Error()
		if msg == "session not found or not dismissed" {
			common.ReplyErr(w, msg, http.StatusNotFound)
			return
		}
		if msg == "another active or waiting session exists for this conversation" {
			common.ReplyErr(w, msg, http.StatusConflict)
			return
		}
		common.ReplyErr(w, "restore failed: "+msg, http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"session_id": sessionID, "dismissed": false})
}

// ListDismissedSessionsHandler handles GET /conversations/{conversation_id}/dismissed-plugin-sessions.
// Returns sessions the user has dismissed, so they can be restored via the UI.
func ListDismissedSessionsHandler(w http.ResponseWriter, r *http.Request) {
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
	sessions, err := ListDismissedSessions(r.Context(), db, convID)
	if err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}
	out := make([]sessionDTO, 0, len(sessions))
	for i := range sessions {
		out = append(out, toSessionDTO(&sessions[i]))
	}
	common.ReplyOK(w, map[string]any{"sessions": out})
}
