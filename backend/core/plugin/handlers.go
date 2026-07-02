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
	"lazymind/core/doc"
	"lazymind/core/store"
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
	IntentContext  string    `json:"intent_context,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Slots          []slotDTO `json:"slots,omitempty"`
	Steps          []stepDTO `json:"steps,omitempty"`
}

// stepDTO summarises one plugin_session_steps row (used for dependency validation).
type stepDTO struct {
	StepID        string    `json:"step_id"`
	Attempt       int       `json:"attempt"`
	TaskID        string    `json:"task_id"`
	Status        string    `json:"status"`
	IntentContext string    `json:"intent_context,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
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
		IntentContext:  s.IntentContext,
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
	// Build step intent map for fast lookup.
	intentMap := buildStepIntentMap(ctx, db, sessionID)
	for i := range steps {
		sd := toStepDTO(&steps[i])
		sd.IntentContext = intentMap[steps[i].StepID]
		dto.Steps = append(dto.Steps, sd)
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

	// Self-healing: if the session appears active but no steps are still running
	// (e.g. the server crashed before updating statuses), repair the state so
	// the frontend doesn't get stuck on "executing".
	if s.Status == SessionStatusActive {
		healStaleActiveSession(r.Context(), db, s)
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
	if sess.Dismissed {
		common.ReplyErr(w, "session is dismissed", http.StatusConflict)
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
