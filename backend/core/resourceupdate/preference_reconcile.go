package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/currentmemory"
	"lazymind/core/maintenance"
)

// PreferenceOrganizerState identifies both the document revision and its budget.
// UpdatedAt distinguishes a later write restoring exactly the same content.
type PreferenceOrganizerState struct {
	currentmemory.PreferenceProjectionState
	ETag      string    `json:"etag"`
	UpdatedAt time.Time `json:"updated_at"`
}

func loadPreferenceOrganizerState(ctx context.Context, db *gorm.DB, userID string, maxChars int) (*PreferenceOrganizerState, error) {
	entry, err := currentmemory.NewRepository(db).GetEntry(ctx, userID, currentmemory.PreferencePath)
	if err != nil {
		return nil, err
	}
	document, err := currentmemory.ParsePreferences(entry.Content)
	if err != nil {
		return nil, fmt.Errorf("invalid preference index: %w", err)
	}
	return &PreferenceOrganizerState{
		PreferenceProjectionState: currentmemory.BuildPreferenceProjectionState(document, maxChars),
		ETag:                      currentmemory.ContentETag(entry.Content),
		UpdatedAt:                 entry.UpdatedAt,
	}, nil
}

func (s *PreferenceOrganizerState) matches(other *PreferenceOrganizerState) bool {
	return s != nil && other != nil && s.ETag == other.ETag && s.MaxChars == other.MaxChars && s.UpdatedAt.Equal(other.UpdatedAt)
}

type preferenceOrganizerResultState struct {
	ObservedState *PreferenceOrganizerState `json:"observed_state"`
	FinalState    *struct {
		ETag     string `json:"etag"`
		MaxChars int    `json:"max_chars"`
	} `json:"final_state"`
}

func latestPreferenceOrganizer(db *gorm.DB, userID string) *gorm.DB {
	return db.Where("user_id = ? AND task_type = ?", userID, orm.ResourceUpdateTaskTypeOrganizePreference).
		Order("CASE WHEN status IN ('pending', 'running') THEN 0 ELSE 1 END ASC").Order("created_at DESC, id DESC")
}

// EnqueuePreferenceOrganizerIfNeeded is shared by immediate writes and the
// background scan. Read, deduplication and admission use the same user lock.
func EnqueuePreferenceOrganizerIfNeeded(ctx context.Context, db *gorm.DB, userID string, maxChars int, now time.Time) (orm.ResourceUpdateTask, bool, error) {
	var task orm.ResourceUpdateTask
	created := false
	err := maintenance.UserTransaction(ctx, db, userID, func(tx *gorm.DB) error {
		state, err := loadPreferenceOrganizerState(ctx, tx, userID, maxChars)
		if err != nil {
			return err
		}
		if state.StoredItems == 0 || !state.NeedsOrganization() {
			return nil
		}
		err = latestPreferenceOrganizer(tx, userID).Take(&task).Error
		if err == nil {
			if task.Status == orm.ResourceUpdateTaskStatusPending || task.Status == orm.ResourceUpdateTaskStatusRunning {
				return nil
			}
			var result preferenceOrganizerResultState
			if json.Unmarshal(task.ResultJSON, &result) == nil && result.ObservedState.matches(state) {
				return nil
			}
			// A failed final read must not reset the worker's retry limit on every
			// scan. Only a later write or a different budget admits a new attempt.
			var request PreferenceOrganizerRequest
			if result.ObservedState == nil && task.Status == orm.ResourceUpdateTaskStatusFailed && task.FinishedAt != nil && json.Unmarshal(task.RequestJSON, &request) == nil {
				attempt := request.ExecutionState
				if attempt == nil {
					attempt = request.EnqueuedState
				}
				if attempt != nil && attempt.MaxChars == state.MaxChars && !state.UpdatedAt.After(*task.FinishedAt) {
					return nil
				}
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		task, created, err = enqueuePreferenceOrganizer(ctx, tx, userID, orm.ResourceUpdateTriggerTypePreferenceChange,
			fmt.Sprintf("preference-check:%s:%s", userID, common.GenerateID()), now, state)
		return err
	})
	return task, created, err
}

func (s *Scanner) scanPreferenceOrganizers(ctx context.Context, now time.Time) (int, error) {
	const batchSize = 100
	maxChars, err := currentmemory.PreferenceContextMaxCharsFromEnv()
	if err != nil {
		return 0, err
	}
	var userIDs []string
	if err := s.db.WithContext(ctx).Model(&orm.MemoryCurrentEntry{}).
		Where("path = ? AND entry_type = ? AND user_id > ?", currentmemory.PreferencePath, currentmemory.EntryFile, s.preferenceAfterUserID).
		Order("user_id ASC").Limit(batchSize).Pluck("user_id", &userIDs).Error; err != nil {
		return 0, err
	}
	created := 0
	for _, userID := range userIDs {
		if ctx.Err() != nil {
			return created, ctx.Err()
		}
		_, admitted, err := EnqueuePreferenceOrganizerIfNeeded(ctx, s.db, userID, maxChars, now)
		if err != nil {
			resourceUpdateWarn("preference_organizer_check_failed", err).Str("user_id", userID).Msg("Preference Organizer check failed")
		} else if admitted {
			created++
		}
		s.preferenceAfterUserID = userID
	}
	if len(userIDs) < batchSize {
		s.preferenceAfterUserID = ""
	}
	return created, nil
}

// Called inside the same user transaction that releases the task's lease and
// preference freeze. A late executor cannot attach a snapshot to a newer run.
func (w *Worker) observePreferenceOrganizerCompletion(ctx context.Context, tx *gorm.DB, task orm.ResourceUpdateTask, updates map[string]any) error {
	var stored orm.ResourceUpdateTask
	if err := tx.Select("request_json").Where("id = ?", task.ID).Take(&stored).Error; err != nil {
		return err
	}
	var request PreferenceOrganizerRequest
	if err := json.Unmarshal(stored.RequestJSON, &request); err != nil {
		return nil
	}
	attempt := request.ExecutionState
	if attempt == nil {
		attempt = request.EnqueuedState
	}
	if attempt == nil {
		return nil
	}
	state, err := loadPreferenceOrganizerState(ctx, tx, task.UserID, attempt.MaxChars)
	if err != nil {
		resourceUpdateWarn("preference_organizer_final_read_failed", err).Str("task_id", task.ID).Msg("Could not observe final preference state")
		return nil
	}
	result := map[string]any{}
	if raw, ok := updates["result_json"].(json.RawMessage); ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
	}
	if result == nil {
		result = map[string]any{}
	}
	result["observed_state"] = state
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	updates["result_json"] = json.RawMessage(raw)
	return nil
}
