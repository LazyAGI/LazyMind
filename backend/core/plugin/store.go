// Package plugin manages plugin sessions, steps, and slot revisions.
// SubAgent tables (sub_agent_tasks / sub_agent_steps / sub_agent_artifacts) are reused unchanged.
package plugin

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

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
//
// cardinality=single: deselects all previous revisions of the same (sessionID, slotID).
//
// cardinality=list, listIndex=nil: appends a new item; list_index = current count.
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
				// Full append: list_index = current count of entries (before this insert).
				var count int64
				if err := tx.Model(&orm.PluginSlotRevision{}).
					Where("session_id = ? AND slot_id = ?", sessionID, slotID).
					Count(&count).Error; err != nil {
					return err
				}
				idx := int(count)
				finalListIndex = &idx
			}
		}

		row := &orm.PluginSlotRevision{
			ID:          "psr_" + common.GenerateID(),
			SessionID:   sessionID,
			SlotID:      slotID,
			Revision:    revision,
			ListIndex:   finalListIndex,
			Selected:    true,
			ArtifactKey: artifactKey,
			StepID:      stepID,
			Attempt:     attempt,
			CreatedAt:   now,
		}
		return tx.Create(row).Error
	}); err != nil {
		return nil, err
	}

	var result orm.PluginSlotRevision
	err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND revision = ?", sessionID, slotID, revision).
		First(&result).Error
	return &result, err
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
