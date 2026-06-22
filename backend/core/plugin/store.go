// Package plugin manages plugin sessions, steps, and slot revisions.
// SubAgent tables (sub_agent_tasks / sub_agent_steps / sub_agent_artifacts) are reused unchanged.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

// Session status constants.
const (
	SessionStatusActive    = "active"
	SessionStatusCompleted = "completed"
	SessionStatusFailed    = "failed"
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

// WriteSlotRevision inserts a new slot revision and manages the selected flag.
// It also updates plugin_slot_order for list slots and writes content_snapshot.
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

	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Compute next revision number across all revisions for this (session, slot).
		var maxRev int
		if err := tx.Model(&orm.PluginSlotRevision{}).
			Select("COALESCE(MAX(revision), 0)").
			Where("session_id = ? AND slot_id = ?", sessionID, slotID).
			Scan(&maxRev).Error; err != nil {
			return err
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

// WriteSlotRevisionWithSnapshot writes a new revision and records content_snapshot atomically.
// Used by PatchSlotItem (human edits) to write version + snapshot in one transaction.
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
		var maxRev int
		if err := tx.Model(&orm.PluginSlotRevision{}).
			Select("COALESCE(MAX(revision), 0)").
			Where("session_id = ? AND slot_id = ?", sessionID, slotID).
			Scan(&maxRev).Error; err != nil {
			return err
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
		// Look up task_ids for this session, then hide artifacts with the matching artifact_key
		// at the row corresponding to this list_index (seq position).
		// We identify the correct artifact_key from the revisions we just deselected.
		var artifactKeys []string
		if err := tx.Model(&orm.PluginSlotRevision{}).
			Select("DISTINCT artifact_key").
			Where("session_id = ? AND slot_id = ? AND list_index = ?", sessionID, slotID, listIndex).
			Pluck("artifact_key", &artifactKeys).Error; err != nil {
			return err
		}
		if len(artifactKeys) > 0 {
			// Get all task_ids for this session.
			var taskIDs []string
			if err := tx.Model(&orm.PluginSessionStep{}).
				Select("DISTINCT task_id").
				Where("session_id = ?", sessionID).
				Pluck("task_id", &taskIDs).Error; err != nil {
				return err
			}
			if len(taskIDs) > 0 {
				// Load all non-hidden artifacts for this session + artifact_key combination.
				// Find only the artifact whose value JSON contains {"list_index": listIndex}.
				var candidates []orm.SubAgentArtifact
				if err := tx.Where("task_id IN ? AND artifact_key IN ? AND hidden = ?", taskIDs, artifactKeys, false).
					Find(&candidates).Error; err != nil {
					return err
				}
				for _, c := range candidates {
					var v map[string]any
					if json.Unmarshal(c.Value, &v) != nil {
						continue
					}
					var li int
					switch raw := v["list_index"].(type) {
					case float64:
						li = int(raw)
					case int:
						li = raw
					default:
						continue
					}
					if li == listIndex {
						if err := tx.Model(&orm.SubAgentArtifact{}).
							Where("task_id = ? AND artifact_key = ? AND seq = ?", c.TaskID, c.ArtifactKey, c.Seq).
							Updates(map[string]any{"hidden": true}).Error; err != nil {
							return err
						}
					}
				}
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
// grouped by slot_id (one entry per slot for single, all entries for list).
func LoadSelectedSlots(ctx context.Context, db *gorm.DB, sessionID string) ([]orm.PluginSlotRevision, error) {
	var rows []orm.PluginSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND selected = ?", sessionID, true).
		Order("slot_id ASC, list_index ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
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
