package workflow

// handlers_by_index.go — slot item handlers addressed by list_index (stable identifier).
//
// These handlers replace the sort_order-based variants for all mutations where
// sort_order is unreliable (delete, patch value, patch caption, get versions, rollback).
//
// URL pattern: /workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}[/...]
//
// list_index is a permanent, monotonically increasing integer assigned when an item is
// first created and never reused.  It is returned in every SlotRevision as "list_index".
// Using it as the address removes the sort_order-drift bug that caused incorrect
// operations after rapid add/delete sequences.
//
// Delete additionally accepts an optional "order_version" body field for optimistic
// locking.  When provided and mismatched, the handler returns 409 so the front-end can
// refresh and retry.  Patch / caption / versions / rollback do not touch order_list so
// they need no version guard.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"

	"gorm.io/gorm"
)

// parseListIndex parses the "list_index" path variable as an integer.
// Returns (n, true) for n >= 0 (list items) or n == -1 (sentinel for single/NULL slots).
// Returns (0, false) on parse error or empty string.
func parseListIndex(r *http.Request) (int, bool) {
	s := common.PathVar(r, "list_index")
	if s == "" {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < -1 {
		return 0, false
	}
	return n, true
}

// DeleteSlotItemByIndex handles DELETE /workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}.
// Body (optional): {"order_version": N}  — when provided, triggers optimistic-lock check.
func DeleteSlotItemByIndex(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	listIndex, ok := parseListIndex(r)
	if !ok || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id, slot_id and list_index required", http.StatusBadRequest)
		return
	}

	// Optional optimistic-lock version in request body.
	var body struct {
		OrderVersion *int `json:"order_version,omitempty"`
	}
	// Ignore decode errors — body is optional.
	_ = json.NewDecoder(r.Body).Decode(&body)

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()

	// If caller provided order_version, verify it matches before mutating.
	if body.OrderVersion != nil {
		row, err := GetSlotOrder(ctx, db, sessionID, slotID)
		if err != nil {
			common.ReplyErr(w, "order lookup failed", http.StatusInternalServerError)
			return
		}
		if row != nil && row.OrderVersion != *body.OrderVersion {
			common.ReplyErr(w, "order_version conflict; refresh and retry", http.StatusConflict)
			return
		}
	}

	if err := HideSlotItem(ctx, db, sessionID, slotID, listIndex); err != nil {
		common.ReplyErr(w, "delete item failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"type":       "slot_item_deleted",
		"session_id": sessionID,
		"slot_id":    slotID,
		"list_index": listIndex,
	})
}

// PatchSlotItemByIndex handles PATCH /workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}.
// Body: {"value": <json>, "content_type": "text"|"json"|"image"|"file", "mode": "draft"|"checkpoint", "base_revision": N, "base_draft_version": N}
//
// mode=draft updates the selected human artifact in place when possible (no new revision).
// mode=checkpoint (default) always creates a new human revision.
func PatchSlotItemByIndex(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	listIndex, ok := parseListIndex(r)
	if !ok || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id, slot_id and list_index required", http.StatusBadRequest)
		return
	}
	var body struct {
		Value            json.RawMessage `json:"value"`
		ContentType      string          `json:"content_type"`
		Caption          *string         `json:"caption"`
		Mode             string          `json:"mode"`
		BaseRevision     *int            `json:"base_revision"`
		BaseDraftVersion *int64          `json:"base_draft_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Value) == 0 {
		common.ReplyErr(w, "invalid body: value required", http.StatusBadRequest)
		return
	}
	if body.ContentType == "" {
		body.ContentType = "text"
	}
	mode := body.Mode
	if mode == "" {
		mode = "checkpoint"
	}
	if mode != "draft" && mode != "checkpoint" {
		common.ReplyErr(w, "invalid mode: must be draft or checkpoint", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	var liPtr *int
	if listIndex >= 0 {
		li := listIndex
		liPtr = &li
	}
	if err := validateSelectedHumanArtifactBaseline(
		ctx, db, sessionID, slotID, liPtr, body.BaseRevision, body.BaseDraftVersion,
	); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "slot revision not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, ErrRevisionRequired) {
			common.ReplyErrWithData(w, "base_revision required", map[string]any{
				"code": "REVISION_REQUIRED",
			}, http.StatusBadRequest)
			return
		}
		if errors.Is(err, ErrConflict) {
			common.ReplyErrWithData(w, "revision conflict; refresh and retry", map[string]any{
				"code": "REVISION_CONFLICT",
			}, http.StatusConflict)
			return
		}
		if replyDraftVersionPreconditionError(w, err) {
			return
		}
		common.ReplyErr(w, "slot revision lookup failed", http.StatusInternalServerError)
		return
	}
	cleaned := resolveValuePaths(body.Value)

	// listIndex == -1 means single slot (list_index IS NULL)
	var existing orm.WorkflowSlotRevision
	q := db.WithContext(ctx).Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true)
	if liPtr == nil {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", *liPtr)
	}
	if err := q.First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErrWithData(w, "revision conflict; refresh and retry", map[string]any{
				"code": "REVISION_CONFLICT",
			}, http.StatusConflict)
			return
		}
		common.ReplyErr(w, "slot revision lookup failed", http.StatusInternalServerError)
		return
	}
	slotType := "single"
	if existing.ListIndex != nil {
		slotType = "list"
	}
	newRev, draftVersion, _, err := SaveHumanArtifactValue(ctx, db,
		sessionID, slotID, existing.Slot, existing.StepID, existing.Attempt,
		slotType,
		liPtr,
		body.ContentType, cleaned, body.Caption, body.BaseRevision,
		body.BaseDraftVersion, mode == "draft",
	)
	if err != nil {
		if replyDraftVersionPreconditionError(w, err) {
			return
		}
		if errors.Is(err, ErrConflict) {
			common.ReplyErrWithData(w, "revision conflict; refresh and retry", map[string]any{
				"code": "REVISION_CONFLICT",
			}, http.StatusConflict)
			return
		}
		common.ReplyErr(w, "patch item failed", http.StatusInternalServerError)
		return
	}
	NotifyWorkflowArtifactUpdated(ctx, db, sessionID, newRev.StepID, newRev.SlotID, newRev.Slot, newRev.Revision, newRev.ListIndex, "human")
	common.ReplyOK(w, map[string]any{
		"type":          "slot_item_patched",
		"session_id":    sessionID,
		"slot_id":       slotID,
		"list_index":    listIndex,
		"revision":      newRev.Revision,
		"draft_version": draftVersion,
		"mode":          mode,
	})
}

func validateSelectedHumanArtifactBaseline(
	ctx context.Context,
	db *gorm.DB,
	sessionID, slotID string,
	listIndex *int,
	baseRevision *int,
	baseDraftVersion *int64,
) error {
	var selected orm.WorkflowSlotRevision
	query := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true)
	if listIndex == nil {
		query = query.Where("list_index IS NULL")
	} else {
		query = query.Where("list_index = ?", *listIndex)
	}
	if err := query.First(&selected).Error; err != nil {
		return err
	}
	if baseRevision == nil || *baseRevision <= 0 {
		return ErrRevisionRequired
	}
	if selected.Revision != *baseRevision {
		return ErrConflict
	}
	if selected.HumanArtifactID == nil || *selected.HumanArtifactID == "" {
		return nil
	}
	if baseDraftVersion == nil {
		return ErrDraftVersionRequired
	}
	return nil
}

func replyDraftVersionPreconditionError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrDraftVersionRequired):
		common.ReplyErrWithData(w, "base_draft_version required", map[string]any{
			"code": "DRAFT_VERSION_REQUIRED",
		}, http.StatusBadRequest)
		return true
	case errors.Is(err, ErrDraftVersionConflict):
		common.ReplyErrWithData(w, "draft version conflict; refresh and retry", map[string]any{
			"code": "DRAFT_VERSION_CONFLICT",
		}, http.StatusConflict)
		return true
	case errors.Is(err, ErrArtifactInUse):
		common.ReplyErrWithData(w, "artifact is in use by a running workflow attempt", map[string]any{
			"code": "ARTIFACT_IN_USE",
		}, http.StatusConflict)
		return true
	default:
		return false
	}
}

// PatchSlotCaptionByIndex handles PATCH /workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/caption.
// Body: {"caption": "..."}
func PatchSlotCaptionByIndex(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	listIndex, ok := parseListIndex(r)
	if !ok || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id, slot_id and list_index required", http.StatusBadRequest)
		return
	}
	var body struct {
		Caption string `json:"caption"`
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
	li := listIndex
	var rev orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND list_index = ? AND selected = ?", sessionID, slotID, li, true).
		First(&rev).Error; err != nil {
		common.ReplyErr(w, "slot revision not found", http.StatusNotFound)
		return
	}
	cap := body.Caption
	if rev.HumanArtifactID != nil {
		if err := db.WithContext(ctx).Model(&orm.WorkflowHumanArtifact{}).
			Where("id = ?", *rev.HumanArtifactID).
			Update("caption", &cap).Error; err != nil {
			common.ReplyErr(w, "update caption failed", http.StatusInternalServerError)
			return
		}
	} else {
		var step orm.WorkflowSessionStep
		if err := db.WithContext(ctx).
			Where("session_id = ? AND step_id = ? AND attempt = ?", sessionID, rev.StepID, rev.Attempt).
			First(&step).Error; err != nil {
			common.ReplyErr(w, "session step not found", http.StatusNotFound)
			return
		}
		if err := db.WithContext(ctx).Model(&orm.SubAgentArtifact{}).
			Where("task_id = ? AND slot = ?", step.TaskID, rev.Slot).
			Update("caption", &cap).Error; err != nil {
			common.ReplyErr(w, "update caption failed", http.StatusInternalServerError)
			return
		}
	}
	common.ReplyOK(w, map[string]any{"status": "ok"})
}

// GetSlotItemVersionsByIndex handles GET /workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions.
func GetSlotItemVersionsByIndex(w http.ResponseWriter, r *http.Request) {
	owner, ok := documentReadOwner(w, r)
	if !ok {
		return
	}
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	listIndex, ok := parseListIndex(r)
	if !ok || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id, slot_id and list_index required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	if !authorizeDocumentSession(w, r, db, sessionID, owner) {
		return
	}
	ctx := r.Context()
	var liPtr *int
	if listIndex >= 0 {
		li := listIndex
		liPtr = &li
	}
	// listIndex == -1 means single slot (list_index IS NULL in DB)
	revisions, err := LoadSlotVersions(ctx, db, sessionID, slotID, liPtr)
	if err != nil {
		common.ReplyErr(w, "query versions failed", http.StatusInternalServerError)
		return
	}

	// Build task_id lookup once for all revisions (avoids N+1 queries).
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

	out := make([]map[string]any, 0, len(revisions))
	formalVersion := 0
	for _, rev := range revisions {
		if isWriterWorkingDraftRevision(slotID, rev.ChangeSource) {
			continue
		}
		formalVersion++
		item := map[string]any{
			"artifact_id":   rev.ID,
			"revision":      rev.Revision,
			"change_source": rev.ChangeSource,
			"created_at":    rev.CreatedAt,
			"selected":      rev.Selected,
		}
		if slotID == "draft_document" || slotID == "flat_draft_document" {
			item["version"] = formalVersion
		}
		var artifactValue json.RawMessage
		if rev.HumanArtifactID != nil {
			var ha orm.WorkflowHumanArtifact
			if db.WithContext(ctx).Where("id = ?", *rev.HumanArtifactID).First(&ha).Error == nil {
				ct := resolveContentType(ha.ContentType, ha.Value)
				item["content_snapshot"] = enrichArtifactValue(ha.Value, ct)
				item["content_type"] = ct
				item["draft_version"] = ha.DraftVersion
				artifactValue = ha.Value
			}
		} else if rev.ArtifactSeq != nil {
			tid := taskIDByStep[stepKey{rev.StepID, rev.Attempt}]
			if tid != "" {
				var art orm.SubAgentArtifact
				if db.WithContext(ctx).
					Where("task_id = ? AND slot = ? AND seq = ?", tid, rev.Slot, *rev.ArtifactSeq).
					First(&art).Error == nil {
					ct := resolveContentType(art.ContentType, art.Value)
					item["content_snapshot"] = enrichArtifactValue(art.Value, ct)
					item["content_type"] = ct
					artifactValue = art.Value
				}
			}
		} else if len(rev.ContentSnapshot) > 0 {
			item["content_snapshot"] = enrichArtifactValue(rev.ContentSnapshot, "")
			artifactValue = rev.ContentSnapshot
		}
		if (slotID == "draft_document" || slotID == "flat_draft_document") &&
			writerSlotRevisionSynced(rev.ChangeSource, artifactValue) {
			item["provider_synced"] = true
		}
		artifact := describeDocumentRevision(ctx, db, owner, rev.ID)
		if artifact.Document != nil {
			item["document"] = artifact.Document
		}
		if artifact.DocumentError != nil {
			item["document_error"] = artifact.DocumentError
		}
		out = append(out, item)
	}
	common.ReplyOK(w, map[string]any{"versions": out})
}

// RollbackSlotItemByIndex handles POST /workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/rollback.
// Body: {"revision": N}
func RollbackSlotItemByIndex(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	listIndex, ok := parseListIndex(r)
	if !ok || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id, slot_id and list_index required", http.StatusBadRequest)
		return
	}
	var body struct {
		Revision int `json:"revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Revision < 1 {
		common.ReplyErr(w, "invalid body: revision >= 1 required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	var liPtr *int
	if listIndex >= 0 {
		li := listIndex
		liPtr = &li
	}
	var anyRev orm.WorkflowSlotRevision
	q := db.WithContext(ctx).Where("session_id = ? AND slot_id = ?", sessionID, slotID)
	if liPtr == nil {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", *liPtr)
	}
	if err := q.First(&anyRev).Error; err != nil {
		common.ReplyErr(w, "slot revision not found", http.StatusNotFound)
		return
	}
	newRev, err := RollbackSlotRevision(ctx, db, sessionID, slotID, liPtr, body.Revision, anyRev.Slot)
	if replyDraftVersionPreconditionError(w, err) {
		return
	}
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "target revision not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "rollback failed", http.StatusInternalServerError)
		return
	}
	NotifyWorkflowArtifactUpdated(ctx, db, sessionID, newRev.StepID, newRev.SlotID, newRev.Slot, newRev.Revision, newRev.ListIndex, "rollback")
	common.ReplyOK(w, map[string]any{
		"type":       "slot_item_rolled_back",
		"session_id": sessionID,
		"slot_id":    slotID,
		"list_index": listIndex,
		"revision":   newRev.Revision,
	})
}
