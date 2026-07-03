package plugin

// handlers_by_index.go — slot item handlers addressed by list_index (stable identifier).
//
// These handlers replace the sort_order-based variants for all mutations where
// sort_order is unreliable (delete, patch value, patch caption, get versions, rollback).
//
// URL pattern: /plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}[/...]
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
	"encoding/json"
	"fmt"
	"net/http"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
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

// DeleteSlotItemByIndex handles DELETE /plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}.
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

// PatchSlotItemByIndex handles PATCH /plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}.
// Body: {"value": <json>, "content_type": "text"|"json"|"image"|"file"}
func PatchSlotItemByIndex(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	slotID := common.PathVar(r, "slot_id")
	listIndex, ok := parseListIndex(r)
	if !ok || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "session_id, slot_id and list_index required", http.StatusBadRequest)
		return
	}
	var body struct {
		Value       json.RawMessage `json:"value"`
		ContentType string          `json:"content_type"`
		Caption     *string         `json:"caption"`
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
	var liPtr *int
	if listIndex >= 0 {
		li := listIndex
		liPtr = &li
	}
	// listIndex == -1 means single slot (list_index IS NULL)
	var existing orm.PluginSlotRevision
	q := db.WithContext(ctx).Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true)
	if liPtr == nil {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", *liPtr)
	}
	if err := q.First(&existing).Error; err != nil {
		common.ReplyErr(w, "slot revision not found", http.StatusNotFound)
		return
	}
	slotType := "single"
	if existing.ListIndex != nil {
		slotType = "list"
	}
	newRev, err := WriteSlotRevisionWithHumanArtifact(ctx, db,
		sessionID, slotID, existing.Slot, existing.StepID, existing.Attempt,
		slotType,
		liPtr,
		body.ContentType, resolveValuePaths(body.Value), body.Caption,
	)
	if err != nil {
		common.ReplyErr(w, "patch item failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"type":       "slot_item_patched",
		"session_id": sessionID,
		"slot_id":    slotID,
		"list_index": listIndex,
		"revision":   newRev.Revision,
	})
}

// PatchSlotCaptionByIndex handles PATCH /plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/caption.
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
	var rev orm.PluginSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND list_index = ? AND selected = ?", sessionID, slotID, li, true).
		First(&rev).Error; err != nil {
		common.ReplyErr(w, "slot revision not found", http.StatusNotFound)
		return
	}
	cap := body.Caption
	if rev.HumanArtifactID != nil {
		if err := db.WithContext(ctx).Model(&orm.PluginHumanArtifact{}).
			Where("id = ?", *rev.HumanArtifactID).
			Update("caption", &cap).Error; err != nil {
			common.ReplyErr(w, "update caption failed", http.StatusInternalServerError)
			return
		}
	} else {
		var step orm.PluginSessionStep
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

// GetSlotItemVersionsByIndex handles GET /plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions.
func GetSlotItemVersionsByIndex(w http.ResponseWriter, r *http.Request) {
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
	var steps []orm.PluginSessionStep
	db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&steps)
	for _, s := range steps {
		taskIDByStep[stepKey{s.StepID, s.Attempt}] = s.TaskID
	}

	out := make([]map[string]any, 0, len(revisions))
	for _, rev := range revisions {
		item := map[string]any{
			"revision":      rev.Revision,
			"change_source": rev.ChangeSource,
			"created_at":    rev.CreatedAt,
			"selected":      rev.Selected,
		}
		if rev.HumanArtifactID != nil {
			var ha orm.PluginHumanArtifact
			if db.WithContext(ctx).Where("id = ?", *rev.HumanArtifactID).First(&ha).Error == nil {
				ct := resolveContentType(ha.ContentType, ha.Value)
				item["content_snapshot"] = signArtifactImagePath(ha.Value, ct)
				item["content_type"] = ct
			}
		} else if rev.ArtifactSeq != nil {
			tid := taskIDByStep[stepKey{rev.StepID, rev.Attempt}]
			if tid != "" {
				var art orm.SubAgentArtifact
				if db.WithContext(ctx).
					Where("task_id = ? AND slot = ? AND seq = ?", tid, rev.Slot, *rev.ArtifactSeq).
					First(&art).Error == nil {
					ct := resolveContentType(art.ContentType, art.Value)
					item["content_snapshot"] = signArtifactImagePath(art.Value, ct)
					item["content_type"] = ct
				}
			}
		} else if len(rev.ContentSnapshot) > 0 {
			item["content_snapshot"] = signArtifactImagePath(rev.ContentSnapshot, "")
		}
		out = append(out, item)
	}
	common.ReplyOK(w, map[string]any{"versions": out})
}

// RollbackSlotItemByIndex handles POST /plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/rollback.
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
	var anyRev orm.PluginSlotRevision
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
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "target revision not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "rollback failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"type":       "slot_item_rolled_back",
		"session_id": sessionID,
		"slot_id":    slotID,
		"list_index": listIndex,
		"revision":   newRev.Revision,
	})
}
