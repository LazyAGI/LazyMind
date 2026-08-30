package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

// resolveValuePaths normalises a human-uploaded value by ensuring it carries a stable
// absolute path when the value contains a local file path.
// Signed URL generation is intentionally NOT done here — signed URLs expire and must
// be generated fresh on every API response (see enrichArtifactValue called from
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

// enrichArtifactValue enriches artifact values with fresh browser-accessible signed URLs.
func enrichArtifactValue(raw json.RawMessage, contentType string) json.RawMessage {
	return subagent.SignArtifactImageValue(contentType, raw)
}

// sessionDTO is the frontend shape for a WorkflowSession.
type sessionDTO struct {
	SessionID      string `json:"session_id"`
	ConversationID string `json:"conversation_id"`
	WorkflowID     string `json:"workflow_id"`
	// PinnedRevisionID is retained for immutable runtime identity; version numbers
	// are intentionally not exposed because they are not a reliable UI run label.
	PinnedRevisionID string    `json:"pinned_revision_id,omitempty"`
	Status           string    `json:"status"`
	CurrentStepID    string    `json:"current_step_id"`
	IntentContext    string    `json:"intent_context,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Slots            []slotDTO `json:"slots,omitempty"`
	Steps            []stepDTO `json:"steps,omitempty"`
}

// stepDTO summarises one plugin_session_steps attempt for UI history.  // workflow-naming: persistence
type stepDTO struct {
	StepID        string    `json:"step_id"`
	Attempt       int       `json:"attempt"`
	TaskID        string    `json:"task_id"`
	Status        string    `json:"status"`
	Validity      string    `json:"validity"`
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
	Slot          string          `json:"slot"`
	CreatedAt     time.Time       `json:"created_at"`
	ContentType   string          `json:"content_type,omitempty"`
	ArtifactValue json.RawMessage `json:"artifact_value,omitempty"`
	Caption       *string         `json:"caption,omitempty"`
	ChangeSource  string          `json:"change_source,omitempty"`
	// Write-back state is calculated from the server-side Writer revision history
	// and source document. It must not be inferred from a locally edited artifact.
	WriteBackReady     bool   `json:"write_back_ready,omitempty"`
	WriteBackDirty     bool   `json:"write_back_dirty,omitempty"`
	WriteBackState     string `json:"write_back_state,omitempty"`
	WriteBackURL       string `json:"write_back_url,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ProviderDocumentID string `json:"provider_document_id,omitempty"`
	LastSyncedRevision *int   `json:"last_synced_revision,omitempty"`
	LastSyncedVersion  *int   `json:"last_synced_version,omitempty"`
	StepID             string `json:"step_id,omitempty"`
	RevisionCount      int    `json:"revision_count,omitempty"`
	VersionNumber      int    `json:"version_number,omitempty"`
	OrderVersion       *int   `json:"order_version,omitempty"`

	// Internal fields — used by enrichSlots, never serialised to the client.
	ArtifactSeq     *int            `json:"-"`
	HumanArtifactID *string         `json:"-"`
	Attempt         int             `json:"-"`
	ContentSnapshot json.RawMessage `json:"-"`
}

func toSessionDTO(s *orm.WorkflowSession) sessionDTO {
	return sessionDTO{
		SessionID:        s.ID,
		ConversationID:   s.ConversationID,
		WorkflowID:       s.WorkflowID,
		PinnedRevisionID: s.WorkflowRevisionID,
		Status:           s.Status,
		CurrentStepID:    s.CurrentStepID,
		IntentContext:    s.IntentContext,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
	}
}

func toStepDTO(r *orm.WorkflowSessionStep) stepDTO {
	return stepDTO{
		StepID:    r.StepID,
		Attempt:   r.Attempt,
		TaskID:    r.TaskID,
		Status:    r.Status,
		Validity:  r.Validity,
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

func toSlotDTO(r *orm.WorkflowSlotRevision) slotDTO {
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
// and OrderVersion on each slotDTO by querying sub_agent_artifacts, plugin_slot_revisions,  // workflow-naming: persistence
// and plugin_slot_order.  // workflow-naming: persistence
// For each revision: look up plugin_session_steps → task_id, then query  // workflow-naming: persistence
// sub_agent_artifacts(task_id, slot) ordered by seq ASC and pick the
// row at position list_index (0-based); for single slots take the latest (seq DESC).
func enrichSlots(ctx context.Context, db *gorm.DB, sessionID string, slots []slotDTO) {
	// Step 1: build a map (step_id, attempt) → task_id
	type stepKey struct {
		stepID  string
		attempt int
	}
	taskIDByStep := map[stepKey]string{}
	var steps []orm.WorkflowSessionStep
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

	// Step 3: load user-visible version counts per (session_id, slot_id,
	// list_index). Writer human revisions are mutable working drafts: keep them
	// for persistence/concurrency, but do not expose them as formal versions.
	revCounts := map[string]int{}
	versionNumbers := map[string]map[int]int{}
	type revisionNumberRow struct {
		SlotID       string `gorm:"column:slot_id"`
		ListIndex    *int   `gorm:"column:list_index"`
		Revision     int    `gorm:"column:revision"`
		ChangeSource string `gorm:"column:change_source"`
	}
	var revisionRows []revisionNumberRow
	db.WithContext(ctx).
		Model(&orm.WorkflowSlotRevision{}).
		Select("slot_id, list_index, revision, change_source").
		Where("session_id = ?", sessionID).
		Order("slot_id ASC, list_index ASC, revision ASC").
		Scan(&revisionRows)
	for _, revision := range revisionRows {
		key := slotRevisionVersionKey(revision.SlotID, revision.ListIndex)
		if !isWriterWorkingDraftRevision(revision.SlotID, revision.ChangeSource) {
			revCounts[key]++
		}
		if versionNumbers[key] == nil {
			versionNumbers[key] = map[int]int{}
		}
		versionNumbers[key][revision.Revision] = revCounts[key]
	}

	// Step 4: load slot order info for order_version and sort_order lookup.
	orderBySlot := map[string]*orm.WorkflowSlotOrder{}
	var orders []orm.WorkflowSlotOrder
	db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&orders)
	for i := range orders {
		orderBySlot[orders[i].SlotID] = &orders[i]
	}

	// Step 5: assign values to each slotDTO
	for i := range slots {
		slot := &slots[i]

		// Unified value resolution (priority order):
		//   1. HumanArtifactID != nil → human revision: read from plugin_human_artifacts.  // workflow-naming: persistence
		//   2. ArtifactSeq != nil     → AI revision: read from sub_agent_artifacts by seq.
		//   3. ContentSnapshot        → legacy fallback (pre-migration rows).
		var resolved json.RawMessage
		var resolvedContentType string
		var resolvedCaption *string

		if slot.HumanArtifactID != nil {
			var ha orm.WorkflowHumanArtifact
			haErr := db.WithContext(ctx).Where("id = ?", *slot.HumanArtifactID).First(&ha).Error
			if haErr == nil {
				resolvedContentType = resolveContentType(ha.ContentType, ha.Value)
				resolved = enrichArtifactValue(ha.Value, resolvedContentType)
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
						resolved = enrichArtifactValue(a.Value, resolvedContentType)
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
			resolved = enrichArtifactValue(slot.ContentSnapshot, "")
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
		rcKey := slotRevisionVersionKey(slot.SlotID, slot.ListIndex)
		slot.RevisionCount = revCounts[rcKey]
		slot.VersionNumber = versionNumbers[rcKey][slot.Revision]

		// sort_order and order_version from plugin_slot_order.  // workflow-naming: persistence
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

	enrichWriterWriteBackSlots(ctx, db, sessionID, slots)
	for i := range slots {
		slot := &slots[i]
		if slot.LastSyncedRevision == nil {
			continue
		}
		key := slotRevisionVersionKey(slot.SlotID, slot.ListIndex)
		if version := versionNumbers[key][*slot.LastSyncedRevision]; version > 0 {
			slot.LastSyncedVersion = &version
		}
	}
}

func slotRevisionVersionKey(slotID string, listIndex *int) string {
	key := slotID + "|"
	if listIndex != nil {
		key += fmt.Sprintf("%d", *listIndex)
	}
	return key
}

func isWriterWorkingDraftRevision(slotID, changeSource string) bool {
	return (slotID == "draft_document" || slotID == "flat_draft_document") && changeSource == "human"
}

// ListConversationSessions handles GET /conversations/{conversation_id}/workflow-sessions.
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

// GetSessionDetail handles GET /workflow-sessions/{session_id}.
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
	revisions, _ := LoadDisplaySlots(ctx, db, sessionID)
	for i := range revisions {
		dto.Slots = append(dto.Slots, toSlotDTO(&revisions[i]))
	}
	enrichSlots(ctx, db, sessionID, dto.Slots)
	// Load attempt history inline for panel controls and audit display.
	steps, _ := ListSteps(ctx, db, sessionID)
	intentMap := buildStepIntentMap(ctx, db, sessionID)
	for i := range steps {
		step := toStepDTO(&steps[i])
		step.IntentContext = intentMap[steps[i].StepID]
		dto.Steps = append(dto.Steps, step)
	}
	common.ReplyOK(w, map[string]any{"session": dto})
}

// GetSessionSlots handles GET /workflow-sessions/{session_id}/slots.
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
	revisions, err := LoadDisplaySlots(ctx, db, sessionID)
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

// GetSessionSteps handles GET /workflow-sessions/{session_id}/steps.
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
		Validity      string `json:"validity"`
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
			Validity:      s.Validity,
			IntentContext: intentMap[s.StepID],
			CreatedAt:     s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			UpdatedAt:     s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	common.ReplyOK(w, map[string]any{"steps": out})
}

// PatchSessionSlot handles PATCH /workflow-sessions/{session_id}/slots/{slot_id}.
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
	if body.SelectedRevision < 1 {
		common.ReplyErr(w, "selected_revision must be >= 1", http.StatusBadRequest)
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
	// This endpoint selects a version of a cardinality=single slot. List items use
	// the list_index rollback endpoint, which avoids an ambiguous revision number.
	var selected orm.WorkflowSlotRevision
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND revision = ? AND validity = ?",
				sessionID, slotID, body.SelectedRevision, "effective").
			First(&selected).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.WorkflowSlotRevision{}).
			Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND selected = ?", sessionID, slotID, true).
			Update("selected", false).Error; err != nil {
			return err
		}
		return tx.Model(&orm.WorkflowSlotRevision{}).
			Where("id = ?", selected.ID).
			Update("selected", true).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "target revision not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "select revision failed", http.StatusInternalServerError)
		return
	}
	NotifyWorkflowArtifactUpdated(ctx, db, sessionID, selected.StepID, selected.SlotID, selected.Slot, selected.Revision, selected.ListIndex, "selection")
	common.ReplyOK(w, map[string]any{"selected_revision": body.SelectedRevision})
}

// GetActiveConversationSession handles GET /conversations/{conversation_id}/workflow-sessions:active.
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
	if s.Status == SessionStatusActive {
		healStaleActiveSession(r.Context(), db, s)
	}
	dto := toSessionDTO(s)
	revisions, _ := LoadDisplaySlots(r.Context(), db, s.ID)
	for i := range revisions {
		dto.Slots = append(dto.Slots, toSlotDTO(&revisions[i]))
	}
	enrichSlots(r.Context(), db, s.ID, dto.Slots)
	common.ReplyOK(w, map[string]any{"session": dto})
}

// GetLatestConversationSession handles GET /conversations/{conversation_id}/workflow-sessions:latest.
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
	revisions, _ := LoadDisplaySlots(r.Context(), db, s.ID)
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

// SyncSessionSearchConfig handles POST /workflow-sessions/{session_id}:sync-search-config.
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

// ReorderSlotItems handles PATCH /workflow-sessions/{session_id}/slots/{slot_id}/order.
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

// GetSlotOrderHandler handles GET /workflow-sessions/{session_id}/slots/{slot_id}/order.
// Returns the order_list and order_version for a slot, used by Python save_artifacts
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

// CreateSlotItem handles POST /workflow-sessions/{session_id}/slots/{slot_id}/items.
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
	var anyRev orm.WorkflowSlotRevision
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
		var step orm.WorkflowSessionStep
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

// SaveArtifactByKey handles POST /workflow-sessions/{session_id}/artifacts.
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

	// Resolve workflow_id from session.
	var sess orm.WorkflowSession
	if err := db.WithContext(ctx).Where("id = ?", sessionID).First(&sess).Error; err != nil {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	if sess.Dismissed {
		common.ReplyErr(w, "session is dismissed", http.StatusConflict)
		return
	}

	// Resolve slot binding for the slot via Python plugin API.
	slotID, cardinality := resolveSlotBinding(sess.WorkflowID, body.Slot)
	if slotID == "" {
		common.ReplyErr(w, fmt.Sprintf("no slot binding: slot %q in plugin %q", body.Slot, sess.WorkflowID), http.StatusBadRequest)
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

// DismissSessionHandler handles POST /workflow-sessions/{session_id}:dismiss.
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
	if session, err := GetSession(r.Context(), db, sessionID); err == nil &&
		!session.Dismissed && session.Status == SessionStatusActive {
		// Reuse the same cancellation path as the visible Stop button so every
		// pending/running parallel SubAgent receives a Python cancel signal. Only
		// do this for the session being dismissed: an older waiting session may
		// share the conversation with a newer active session.
		stopWorkflowSession(r.Context(), db, store.State(), session)
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

// RestoreSessionHandler handles POST /workflow-sessions/{session_id}:restore.
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

// ListDismissedSessionsHandler handles GET /conversations/{conversation_id}/dismissed-workflow-sessions.
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
