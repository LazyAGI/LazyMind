// Package plugin manages plugin sessions, steps, and slot revisions.
// SubAgent tables (sub_agent_tasks / sub_agent_steps / sub_agent_artifacts) are reused unchanged.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

// Session status constants. Only three states are valid in the new state machine:
// active (SubAgent running), waiting (awaiting user input), completed (session ended).
// The "failed" and "interrupted" statuses are retired — they are now attributes of
// individual sub_agent_tasks, not of the session itself.
const (
	SessionStatusActive    = "active"
	SessionStatusCompleted = "completed"
	SessionStatusWaiting   = "waiting"
)

// Step status mirrors sub_agent_tasks.status.
const (
	StepStatusPending     = "pending"
	StepStatusRunning     = "running"
	StepStatusSucceeded   = "succeeded"
	StepStatusFailed      = "failed"
	StepStatusInterrupted = "interrupted"
)

// CreateSessionInput holds fields required to insert a new plugin_sessions row.
type CreateSessionInput struct {
	SessionID        string
	ConversationID   string
	PluginID         string
	TriggerHistoryID string
	CurrentStepID    string
	CreateUserID     string
}

// CreateSession inserts a new plugin_sessions record.
// It returns an error if an active session already exists for the conversation.
func CreateSession(ctx context.Context, db *gorm.DB, in CreateSessionInput) (*orm.PluginSession, error) {
	// Guard: at most one active session per conversation.
	var count int64
	if err := db.WithContext(ctx).Model(&orm.PluginSession{}).
		Where("conversation_id = ? AND status = ?", in.ConversationID, SessionStatusActive).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("active plugin session already exists for conversation")
	}

	now := time.Now().UTC()
	s := &orm.PluginSession{
		ID:               in.SessionID,
		ConversationID:   in.ConversationID,
		PluginID:         in.PluginID,
		TriggerHistoryID: in.TriggerHistoryID,
		Status:           SessionStatusActive,
		CurrentStepID:    in.CurrentStepID,
		CreateUserID:     in.CreateUserID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := db.WithContext(ctx).Create(s).Error; err != nil {
		return nil, err
	}
	return s, nil
}

// GetActiveSession returns the in-progress plugin session for a conversation, or nil if none.
// Only 'active' status is considered: used by HandlePluginStepCreated to guard against
// duplicate cold-start sessions.
func GetActiveSession(ctx context.Context, db *gorm.DB, conversationID string) (*orm.PluginSession, error) {
	var s orm.PluginSession
	err := db.WithContext(ctx).
		Where("conversation_id = ? AND status = ?", conversationID, SessionStatusActive).
		Order("created_at DESC").
		First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetLatestSession returns the most recent plugin session for a conversation regardless of status,
// or nil if none exists. Used by the frontend to always show session output even after completion.
func GetLatestSession(ctx context.Context, db *gorm.DB, conversationID string) (*orm.PluginSession, error) {
	var s orm.PluginSession
	err := db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("created_at DESC").
		First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSession loads a session by ID.
func GetSession(ctx context.Context, db *gorm.DB, sessionID string) (*orm.PluginSession, error) {
	var s orm.PluginSession
	if err := db.WithContext(ctx).Where("id = ?", sessionID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSessions returns sessions for a conversation ordered by creation time desc.
func ListSessions(ctx context.Context, db *gorm.DB, conversationID string) ([]orm.PluginSession, error) {
	var rows []orm.PluginSession
	if err := db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateSessionStatus transitions a session to a new status.
func UpdateSessionStatus(ctx context.Context, db *gorm.DB, sessionID, status string) error {
	return db.WithContext(ctx).Model(&orm.PluginSession{}).
		Where("id = ?", sessionID).
		Updates(map[string]any{
			"status":     status,
			"updated_at": time.Now().UTC(),
		}).Error
}

// UpdateSessionCurrentStep updates current_step_id for a session.
func UpdateSessionCurrentStep(ctx context.Context, db *gorm.DB, sessionID, stepID string) error {
	return db.WithContext(ctx).Model(&orm.PluginSession{}).
		Where("id = ?", sessionID).
		Updates(map[string]any{
			"current_step_id": stepID,
			"updated_at":      time.Now().UTC(),
		}).Error
}

// CreateSessionStep inserts a new plugin_session_steps record.
func CreateSessionStep(ctx context.Context, db *gorm.DB, sessionID, stepID, taskID string, attempt int) (*orm.PluginSessionStep, error) {
	now := time.Now().UTC()
	row := &orm.PluginSessionStep{
		ID:        "pss_" + common.GenerateID(),
		SessionID: sessionID,
		StepID:    stepID,
		Attempt:   attempt,
		TaskID:    taskID,
		Status:    StepStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateStepStatus mirrors sub_agent_tasks.status changes into plugin_session_steps.
func UpdateStepStatus(ctx context.Context, db *gorm.DB, taskID, status string) error {
	return db.WithContext(ctx).Model(&orm.PluginSessionStep{}).
		Where("task_id = ?", taskID).
		Updates(map[string]any{
			"status":     status,
			"updated_at": time.Now().UTC(),
		}).Error
}

// GetLatestStep returns the most recent execution instance of step_id within a session.
func GetLatestStep(ctx context.Context, db *gorm.DB, sessionID, stepID string) (*orm.PluginSessionStep, error) {
	var row orm.PluginSessionStep
	err := db.WithContext(ctx).
		Where("session_id = ? AND step_id = ?", sessionID, stepID).
		Order("attempt DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

// GetStepByTaskID returns the plugin_session_steps row for a given task_id.
func GetStepByTaskID(ctx context.Context, db *gorm.DB, taskID string) (*orm.PluginSessionStep, error) {
	var row orm.PluginSessionStep
	err := db.WithContext(ctx).Where("task_id = ?", taskID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

// NextAttempt returns the next attempt number for (sessionID, stepID).
func NextAttempt(ctx context.Context, db *gorm.DB, sessionID, stepID string) (int, error) {
	var maxAttempt int
	row := db.WithContext(ctx).Model(&orm.PluginSessionStep{}).
		Select("COALESCE(MAX(attempt), 0)").
		Where("session_id = ? AND step_id = ?", sessionID, stepID)
	if err := row.Scan(&maxAttempt).Error; err != nil {
		return 1, err
	}
	return maxAttempt + 1, nil
}

// ListSteps returns all step records for a session ordered by creation time.
func ListSteps(ctx context.Context, db *gorm.DB, sessionID string) ([]orm.PluginSessionStep, error) {
	var rows []orm.PluginSessionStep
	if err := db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// IsEndStepLatest reports whether the most recently created step in the session has
// step_id == "__end__". This is the canonical way to decide whether a session should be
// considered completed vs. waiting: if the user rolls back by triggering a new step after
// __end__, the __end__ record remains but is no longer the latest, so this returns false.
func IsEndStepLatest(ctx context.Context, db *gorm.DB, sessionID string) (bool, error) {
	var step orm.PluginSessionStep
	err := db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("created_at DESC").
		First(&step).Error
	if err != nil {
		return false, err
	}
	return step.StepID == "__end__", nil
}

// WriteSlotRevision inserts a new AI slot revision and manages the selected flag.
// It resolves artifact_seq by querying the most-recent sub_agent_artifacts row for
// (taskID, artifactKey) and storing its seq as a pointer — the value is never copied
// into content_snapshot for AI revisions.
//
// cardinality=single: deselects all previous revisions of the same (sessionID, slotID).
//
// cardinality=list, listIndex=nil: appends a new item; list_index = MAX(all existing)+1.
//
// cardinality=list, listIndex!=nil: partial retry — replaces the revision at the given
// list_index by deselecting the old row for that index and inserting a new selected row.
// Revisions at other indices are untouched.
func WriteSlotRevision(ctx context.Context, db *gorm.DB,
	sessionID, slotID, artifactKey, stepID string, attempt int,
	cardinality string, listIndex *int) (*orm.PluginSlotRevision, error) {

	now := time.Now().UTC()
	var revision int
	var finalListIndex *int

	// Resolve artifact_seq: find the task_id for this step attempt, then pick
	// the latest seq from sub_agent_artifacts for (task_id, artifact_key).
	// This is best-effort; a nil artifactSeq causes enrichSlots to fall back
	// to content_snapshot (written later by OnSubAgentDoneSnapshot).
	var artifactSeq *int
	var step orm.PluginSessionStep
	if db.WithContext(ctx).
		Where("session_id = ? AND step_id = ? AND attempt = ?", sessionID, stepID, attempt).
		First(&step).Error == nil {
		var art orm.SubAgentArtifact
		if db.WithContext(ctx).
			Where("task_id = ? AND artifact_key = ?", step.TaskID, artifactKey).
			Order("seq DESC").
			First(&art).Error == nil {
			seq := art.Seq
			artifactSeq = &seq
		}
	}

	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Compute next revision number scoped to (session, slot, list_index) so each
		// list item has its own independent version counter starting at 1.
		// For a new list append (listIndex == nil), this is always the first revision.
		var maxRev int
		if cardinality != "list" || listIndex != nil {
			q := tx.Model(&orm.PluginSlotRevision{}).
				Select("COALESCE(MAX(revision), 0)").
				Where("session_id = ? AND slot_id = ?", sessionID, slotID)
			if cardinality == "list" && listIndex != nil {
				q = q.Where("list_index = ?", *listIndex)
			} else {
				q = q.Where("list_index IS NULL")
			}
			if err := q.Scan(&maxRev).Error; err != nil {
				return err
			}
		}
		revision = maxRev + 1

		if cardinality == "single" {
			// Deselect all previous revisions for this slot.
			if err := tx.Model(&orm.PluginSlotRevision{}).
				Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true).
				Update("selected", false).Error; err != nil {
				return err
			}
		} else {
			// list cardinality.
			if listIndex != nil {
				// Partial retry: deselect the existing selected row for this list_index only.
				if err := tx.Model(&orm.PluginSlotRevision{}).
					Where("session_id = ? AND slot_id = ? AND list_index = ? AND selected = ?",
						sessionID, slotID, *listIndex, true).
					Update("selected", false).Error; err != nil {
					return err
				}
				finalListIndex = listIndex
			} else {
				// Full append: list_index = MAX(all existing list_index) + 1 (never reuse deleted indices).
				var maxIdx int
				if err := tx.Model(&orm.PluginSlotRevision{}).
					Select("COALESCE(MAX(list_index), -1)").
					Where("session_id = ? AND slot_id = ?", sessionID, slotID).
					Scan(&maxIdx).Error; err != nil {
					return err
				}
				idx := maxIdx + 1
				finalListIndex = &idx
			}
		}

		row := &orm.PluginSlotRevision{
			ID:           "psr_" + common.GenerateID(),
			SessionID:    sessionID,
			SlotID:       slotID,
			Revision:     revision,
			ListIndex:    finalListIndex,
			Selected:     true,
			ChangeSource: "ai",
			ArtifactSeq:  artifactSeq,
			ArtifactKey:  artifactKey,
			StepID:       stepID,
			Attempt:      attempt,
			CreatedAt:    now,
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}

		// Maintain plugin_slot_order for list slots: append new list_index if not a partial retry.
		if cardinality == "list" && listIndex == nil && finalListIndex != nil {
			if err := appendSlotOrderEntry(ctx, tx, sessionID, slotID, *finalListIndex); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return nil, err
	}

	var result orm.PluginSlotRevision
	err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND revision = ?", sessionID, slotID, revision).
		First(&result).Error
	return &result, err
}

// WriteSlotRevisionWithSnapshot writes a new human revision and records content_snapshot
// atomically. Used by PatchSlotItemByIndex (human edits).
// ArtifactSeq is intentionally left nil — human revisions carry their value in
// ContentSnapshot; the unified read path in enrichSlots falls back to ContentSnapshot
// when ArtifactSeq is nil.
func WriteSlotRevisionWithSnapshot(ctx context.Context, db *gorm.DB,
	sessionID, slotID, artifactKey, stepID string, attempt int,
	cardinality string, listIndex *int,
	contentSnapshot json.RawMessage, changeSource string) (*orm.PluginSlotRevision, error) {

	src := changeSource
	if src == "" {
		src = "ai"
	}

	now := time.Now().UTC()
	var revision int
	var finalListIndex *int

	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Compute next revision number scoped to (session, slot, list_index) so each
		// list item has its own independent version counter starting at 1.
		// For a new list append (listIndex == nil), this is always the first revision.
		var maxRev int
		if cardinality != "list" || listIndex != nil {
			q := tx.Model(&orm.PluginSlotRevision{}).
				Select("COALESCE(MAX(revision), 0)").
				Where("session_id = ? AND slot_id = ?", sessionID, slotID)
			if cardinality == "list" && listIndex != nil {
				q = q.Where("list_index = ?", *listIndex)
			} else {
				q = q.Where("list_index IS NULL")
			}
			if err := q.Scan(&maxRev).Error; err != nil {
				return err
			}
		}
		revision = maxRev + 1

		if cardinality == "single" {
			if err := tx.Model(&orm.PluginSlotRevision{}).
				Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true).
				Update("selected", false).Error; err != nil {
				return err
			}
		} else {
			if listIndex != nil {
				if err := tx.Model(&orm.PluginSlotRevision{}).
					Where("session_id = ? AND slot_id = ? AND list_index = ? AND selected = ?",
						sessionID, slotID, *listIndex, true).
					Update("selected", false).Error; err != nil {
					return err
				}
				finalListIndex = listIndex
			} else {
				var maxIdx int
				if err := tx.Model(&orm.PluginSlotRevision{}).
					Select("COALESCE(MAX(list_index), -1)").
					Where("session_id = ? AND slot_id = ?", sessionID, slotID).
					Scan(&maxIdx).Error; err != nil {
					return err
				}
				idx := maxIdx + 1
				finalListIndex = &idx
			}
		}

		row := &orm.PluginSlotRevision{
			ID:              "psr_" + common.GenerateID(),
			SessionID:       sessionID,
			SlotID:          slotID,
			Revision:        revision,
			ListIndex:       finalListIndex,
			Selected:        true,
			ChangeSource:    src,
			ContentSnapshot: contentSnapshot,
			ArtifactKey:     artifactKey,
			StepID:          stepID,
			Attempt:         attempt,
			CreatedAt:       now,
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}

		if cardinality == "list" && listIndex == nil && finalListIndex != nil {
			if err := appendSlotOrderEntry(ctx, tx, sessionID, slotID, *finalListIndex); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var result orm.PluginSlotRevision
	err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND revision = ?", sessionID, slotID, revision).
		First(&result).Error
	return &result, err
}

// appendSlotOrderEntry adds idx to the end of plugin_slot_order.order_list for the slot.
// Must be called from within an existing transaction; db should be the tx handle.
// Uses SELECT FOR UPDATE to prevent concurrent appends from losing updates.
func appendSlotOrderEntry(ctx context.Context, db *gorm.DB, sessionID, slotID string, idx int) error {
	now := time.Now().UTC()
	var existing orm.PluginSlotOrder
	err := db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("session_id = ? AND slot_id = ?", sessionID, slotID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		list, _ := json.Marshal([]int{idx})
		row := orm.PluginSlotOrder{
			SessionID:    sessionID,
			SlotID:       slotID,
			OrderList:    list,
			OrderVersion: 0,
			UpdatedAt:    now,
		}
		return db.WithContext(ctx).Create(&row).Error
	}
	if err != nil {
		return err
	}
	var current []int
	_ = json.Unmarshal(existing.OrderList, &current)
	// Avoid duplicates (idempotent on retry).
	for _, v := range current {
		if v == idx {
			return nil
		}
	}
	current = append(current, idx)
	newList, _ := json.Marshal(current)
	return db.WithContext(ctx).Model(&orm.PluginSlotOrder{}).
		Where("session_id = ? AND slot_id = ?", sessionID, slotID).
		Updates(map[string]any{
			"order_list":    newList,
			"order_version": existing.OrderVersion + 1,
			"updated_at":    now,
		}).Error
}

// GetSlotOrder returns the plugin_slot_order row for a slot, or nil if not found.
func GetSlotOrder(ctx context.Context, db *gorm.DB, sessionID, slotID string) (*orm.PluginSlotOrder, error) {
	var row orm.PluginSlotOrder
	err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ?", sessionID, slotID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

// ReorderSlot atomically replaces order_list with a new permutation.
// sortOrderSeq is the desired new sequence of sort_order values (1-based) computed from
// the current order; the caller must have already translated them to list_index values.
// version is used for optimistic locking; a mismatch returns ErrConflict.
var ErrConflict = errors.New("version conflict")

func ReorderSlot(ctx context.Context, db *gorm.DB,
	sessionID, slotID string, newListIndexOrder []int, version int) error {

	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing orm.PluginSlotOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("session_id = ? AND slot_id = ?", sessionID, slotID).
			First(&existing).Error; err != nil {
			return err
		}
		if existing.OrderVersion != version {
			return ErrConflict
		}
		// Validate: none of the provided list_index values should correspond to a hidden item.
		// A hidden item's list_index is absent from plugin_slot_order.order_list, so if the
		// caller's newListIndexOrder contains any list_index that is NOT in the current
		// (just-locked) order_list, reject. This is the correct guard: hidden items were
		// already removed from order_list by HideSlotItem.
		currentList := existing.OrderList
		var currentListIndexes []int
		_ = json.Unmarshal(currentList, &currentListIndexes)
		currentSet := make(map[int]struct{}, len(currentListIndexes))
		for _, v := range currentListIndexes {
			currentSet[v] = struct{}{}
		}
		for _, li := range newListIndexOrder {
			if _, ok := currentSet[li]; !ok {
				return errors.New("order list contains hidden or unknown list_index")
			}
		}
		newList, _ := json.Marshal(newListIndexOrder)
		return tx.Model(&orm.PluginSlotOrder{}).
			Where("session_id = ? AND slot_id = ?", sessionID, slotID).
			Updates(map[string]any{
				"order_list":    newList,
				"order_version": existing.OrderVersion + 1,
				"updated_at":    now,
			}).Error
	})
}

// HideSlotItem logically deletes the revision at list_index and removes it from order_list.
// It sets hidden=TRUE on all sub_agent_artifacts rows that share the same (task_id, artifact_key)
// and are associated with this session/slot/list_index, and deselects all plugin_slot_revisions rows.
func HideSlotItem(ctx context.Context, db *gorm.DB, sessionID, slotID string, listIndex int) error {
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Deselect all revisions at this list_index.
		if err := tx.Model(&orm.PluginSlotRevision{}).
			Where("session_id = ? AND slot_id = ? AND list_index = ?", sessionID, slotID, listIndex).
			Updates(map[string]any{"selected": false}).Error; err != nil {
			return err
		}

		// Mark the corresponding sub_agent_artifacts rows as hidden.
		// We collect the artifact_seq values recorded in plugin_slot_revisions for this
		// list_index, then hide exactly those sub_agent_artifacts rows by (task_id, artifact_key, seq).
		// This avoids the old value-JSON matching approach which could incorrectly hide artifacts
		// whose value happened to carry the same list_index number (a write-time ordinal, not the
		// stable DB list_index).
		type artifactRef struct {
			taskID      string
			artifactKey string
			seq         int
		}
		var refs []artifactRef
		var revRows []orm.PluginSlotRevision
		if err := tx.Where("session_id = ? AND slot_id = ? AND list_index = ?", sessionID, slotID, listIndex).
			Find(&revRows).Error; err != nil {
			return err
		}
		// Build task_id lookup for this session.
		var stepRows []orm.PluginSessionStep
		if err := tx.Where("session_id = ?", sessionID).Find(&stepRows).Error; err != nil {
			return err
		}
		taskByStep := map[string]string{}
		for _, s := range stepRows {
			taskByStep[s.StepID+"/"+fmt.Sprint(s.Attempt)] = s.TaskID
		}
		for _, rev := range revRows {
			if rev.ArtifactSeq == nil {
				continue
			}
			tid := taskByStep[rev.StepID+"/"+fmt.Sprint(rev.Attempt)]
			if tid == "" {
				continue
			}
			refs = append(refs, artifactRef{taskID: tid, artifactKey: rev.ArtifactKey, seq: *rev.ArtifactSeq})
		}
		for _, ref := range refs {
			if err := tx.Model(&orm.SubAgentArtifact{}).
				Where("task_id = ? AND artifact_key = ? AND seq = ?", ref.taskID, ref.artifactKey, ref.seq).
				Updates(map[string]any{"hidden": true}).Error; err != nil {
				return err
			}
		}

		// Remove list_index from order_list.
		var existing orm.PluginSlotOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("session_id = ? AND slot_id = ?", sessionID, slotID).
			First(&existing).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existing.SessionID != "" {
			var current []int
			_ = json.Unmarshal(existing.OrderList, &current)
			filtered := current[:0]
			for _, v := range current {
				if v != listIndex {
					filtered = append(filtered, v)
				}
			}
			newList, _ := json.Marshal(filtered)
			if err := tx.Model(&orm.PluginSlotOrder{}).
				Where("session_id = ? AND slot_id = ?", sessionID, slotID).
				Updates(map[string]any{
					"order_list":    newList,
					"order_version": existing.OrderVersion + 1,
					"updated_at":    now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SortOrderToListIndex converts a 1-based sort_order to the list_index stored in plugin_slot_order.
// Returns -1 if not found.
func SortOrderToListIndex(ctx context.Context, db *gorm.DB, sessionID, slotID string, sortOrder int) (int, error) {
	row, err := GetSlotOrder(ctx, db, sessionID, slotID)
	if err != nil {
		return -1, err
	}
	if row == nil {
		return -1, nil
	}
	var list []int
	if err := json.Unmarshal(row.OrderList, &list); err != nil {
		return -1, err
	}
	if sortOrder < 1 || sortOrder > len(list) {
		return -1, nil
	}
	return list[sortOrder-1], nil
}

// ListIndexToSortOrder converts a list_index to its current 1-based sort_order.
// Returns -1 if not found (e.g. item is hidden).
func ListIndexToSortOrder(ctx context.Context, db *gorm.DB, sessionID, slotID string, listIndex int) (int, error) {
	row, err := GetSlotOrder(ctx, db, sessionID, slotID)
	if err != nil {
		return -1, err
	}
	if row == nil {
		return -1, nil
	}
	var list []int
	if err := json.Unmarshal(row.OrderList, &list); err != nil {
		return -1, err
	}
	for i, idx := range list {
		if idx == listIndex {
			return i + 1, nil
		}
	}
	return -1, nil
}

// LoadSlotVersions returns all revisions for (sessionID, slotID, listIndex) ordered by revision ASC.
func LoadSlotVersions(ctx context.Context, db *gorm.DB,
	sessionID, slotID string, listIndex *int) ([]orm.PluginSlotRevision, error) {
	q := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ?", sessionID, slotID)
	if listIndex == nil {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", *listIndex)
	}
	var rows []orm.PluginSlotRevision
	if err := q.Order("revision ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// RollbackSlotRevision switches the selected flag to the target revision.
// The target revision becomes selected=TRUE; the previously selected revision becomes selected=FALSE.
// No new revision row is created.
func RollbackSlotRevision(ctx context.Context, db *gorm.DB,
	sessionID, slotID string, listIndex *int,
	targetRevision int, _ string) (*orm.PluginSlotRevision, error) {

	// Load the target revision to verify it exists.
	tq := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND revision = ?", sessionID, slotID, targetRevision)
	if listIndex == nil {
		tq = tq.Where("list_index IS NULL")
	} else {
		tq = tq.Where("list_index = ?", *listIndex)
	}
	var target orm.PluginSlotRevision
	if err := tq.First(&target).Error; err != nil {
		return nil, err
	}

	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Deselect current selected revision.
		deselectQ := tx.Model(&orm.PluginSlotRevision{}).
			Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true)
		if listIndex == nil {
			deselectQ = deselectQ.Where("list_index IS NULL")
		} else {
			deselectQ = deselectQ.Where("list_index = ?", *listIndex)
		}
		if err := deselectQ.Update("selected", false).Error; err != nil {
			return err
		}

		// Select the target revision.
		return tx.Model(&orm.PluginSlotRevision{}).
			Where("id = ?", target.ID).
			Update("selected", true).Error
	}); err != nil {
		return nil, err
	}

	target.Selected = true
	return &target, nil
}

// LoadSelectedSlots returns the currently-selected slot revisions for a session,
// ordered by (slot_id, sort_order) derived from plugin_slot_order.order_list.
// Falls back to list_index ASC for slots that have no order row.
func LoadSelectedSlots(ctx context.Context, db *gorm.DB, sessionID string) ([]orm.PluginSlotRevision, error) {
	var rows []orm.PluginSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND selected = ?", sessionID, true).
		Order("slot_id ASC, list_index ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	// Re-sort each slot's items by their position in plugin_slot_order.order_list.
	var orders []orm.PluginSlotOrder
	if err := db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&orders).Error; err != nil {
		// On error fall back to the already-loaded list_index order.
		return rows, nil
	}
	orderListBySlot := map[string][]int{}
	for i := range orders {
		var list []int
		if err := json.Unmarshal(orders[i].OrderList, &list); err == nil {
			orderListBySlot[orders[i].SlotID] = list
		}
	}
	if len(orderListBySlot) == 0 {
		return rows, nil
	}

	// Build position map: slotID + list_index → sort_order (1-based).
	type posKey struct {
		slotID    string
		listIndex int
	}
	pos := map[posKey]int{}
	for slotID, list := range orderListBySlot {
		for i, li := range list {
			pos[posKey{slotID, li}] = i + 1
		}
	}

	// Group rows by slot_id, re-order each group, then flatten.
	type group struct {
		slotID string
		items  []orm.PluginSlotRevision
	}
	var groups []group
	slotIdx := map[string]int{}
	for _, row := range rows {
		if idx, ok := slotIdx[row.SlotID]; ok {
			groups[idx].items = append(groups[idx].items, row)
		} else {
			slotIdx[row.SlotID] = len(groups)
			groups = append(groups, group{slotID: row.SlotID, items: []orm.PluginSlotRevision{row}})
		}
	}
	for g := range groups {
		slotID := groups[g].slotID
		if _, hasOrder := orderListBySlot[slotID]; !hasOrder {
			continue
		}
		sort.Slice(groups[g].items, func(i, j int) bool {
			li := 0
			if groups[g].items[i].ListIndex != nil {
				li = *groups[g].items[i].ListIndex
			}
			lj := 0
			if groups[g].items[j].ListIndex != nil {
				lj = *groups[g].items[j].ListIndex
			}
			pi := pos[posKey{slotID, li}]
			pj := pos[posKey{slotID, lj}]
			if pi != pj {
				return pi < pj
			}
			return li < lj
		})
	}
	result := make([]orm.PluginSlotRevision, 0, len(rows))
	for _, g := range groups {
		result = append(result, g.items...)
	}
	return result, nil
}

// IsNotFound reports whether err is a gorm record-not-found error.
func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// ResolveContentType returns the true render content type for an artifact.
//
// The DB content_type column is authoritative:
//   - "text", "image", "html", "json", etc. → returned as-is.
//   - "file" → the value column is JSON {"type":"<real>","path":"...","size":N}
//     where "type" carries the actual content type (e.g. "text", "json", "pdf", "pptx").
//     Parse the JSON and return value["type"], falling back to "file" if absent.
//
// snapshot is the raw artifact value bytes (stored in content_snapshot or read directly
// from sub_agent_artifacts.value).
func ResolveContentType(contentType string, snapshot []byte) string {
	return resolveContentType(contentType, snapshot)
}

// resolveContentType is the internal implementation of ResolveContentType.
func resolveContentType(contentType string, snapshot []byte) string {
	if contentType != "file" {
		return contentType
	}
	// content_type == "file": parse the JSON value to get the real type.
	if len(snapshot) == 0 {
		return "file"
	}
	var v map[string]any
	if json.Unmarshal(snapshot, &v) != nil {
		return "file"
	}
	if t, ok := v["type"].(string); ok && t != "" {
		return t
	}
	return "file"
}

// WriteSlotRevisionWithHumanArtifact inserts a plugin_human_artifacts row and a new
// 'human' slot revision that points to it.  This is the write path for PatchSlotItemByIndex.
//
// contentType must be the explicit type declared by the caller ('text','json','image','file').
// value is the cleaned artifact value (path-only for files/images, inline for text/json).
// caption is optional and stored on the artifact row only.
func WriteSlotRevisionWithHumanArtifact(
	ctx context.Context, db *gorm.DB,
	sessionID, slotID, artifactKey, stepID string, attempt int,
	cardinality string, listIndex *int,
	contentType string, value json.RawMessage, caption *string,
) (*orm.PluginSlotRevision, error) {

	now := time.Now().UTC()
	artifactID := "pha_" + common.GenerateID()
	humanArt := &orm.PluginHumanArtifact{
		ID:          artifactID,
		SessionID:   sessionID,
		ArtifactKey: artifactKey,
		ContentType: contentType,
		Value:       value,
		Caption:     caption,
		CreatedAt:   now,
	}

	var revision int
	var finalListIndex *int

	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(humanArt).Error; err != nil {
			return err
		}
		var maxRev int
		if cardinality != "list" || listIndex != nil {
			q := tx.Model(&orm.PluginSlotRevision{}).
				Select("COALESCE(MAX(revision), 0)").
				Where("session_id = ? AND slot_id = ?", sessionID, slotID)
			if cardinality == "list" && listIndex != nil {
				q = q.Where("list_index = ?", *listIndex)
			} else {
				q = q.Where("list_index IS NULL")
			}
			if err := q.Scan(&maxRev).Error; err != nil {
				return err
			}
		}
		revision = maxRev + 1
		if cardinality == "single" {
			if err := tx.Model(&orm.PluginSlotRevision{}).
				Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true).
				Update("selected", false).Error; err != nil {
				return err
			}
		} else {
			if listIndex != nil {
				if err := tx.Model(&orm.PluginSlotRevision{}).
					Where("session_id = ? AND slot_id = ? AND list_index = ? AND selected = ?",
						sessionID, slotID, *listIndex, true).
					Update("selected", false).Error; err != nil {
					return err
				}
				finalListIndex = listIndex
			} else {
				var maxIdx int
				if err := tx.Model(&orm.PluginSlotRevision{}).
					Select("COALESCE(MAX(list_index), -1)").
					Where("session_id = ? AND slot_id = ?", sessionID, slotID).
					Scan(&maxIdx).Error; err != nil {
					return err
				}
				idx := maxIdx + 1
				finalListIndex = &idx
			}
		}
		row := &orm.PluginSlotRevision{
			ID:              "psr_" + common.GenerateID(),
			SessionID:       sessionID,
			SlotID:          slotID,
			Revision:        revision,
			ListIndex:       finalListIndex,
			Selected:        true,
			ChangeSource:    "human",
			HumanArtifactID: &artifactID,
			ArtifactKey:     artifactKey,
			StepID:          stepID,
			Attempt:         attempt,
			CreatedAt:       now,
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if cardinality == "list" && listIndex == nil && finalListIndex != nil {
			if err := appendSlotOrderEntry(ctx, tx, sessionID, slotID, *finalListIndex); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var result orm.PluginSlotRevision
	err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND revision = ?", sessionID, slotID, revision).
		First(&result).Error
	return &result, err
}
