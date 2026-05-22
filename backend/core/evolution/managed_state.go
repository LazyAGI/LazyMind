package evolution

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const (
	ManagedMemoryTitle     = "智能体工作记忆"
	ManagedPreferenceTitle = "用户画像"
)

type ManagedStateItem struct {
	ResourceID                  string `json:"resource_id"`
	ResourceType                string `json:"resource_type"`
	Title                       string `json:"title"`
	Content                     string `json:"content"`
	ContentSummary              string `json:"content_summary"`
	HasPendingReviewSuggestions bool   `json:"has_pending_review_suggestions"`
	SuggestionStatus            string `json:"suggestion_status"`
	AutoEvo                     bool   `json:"auto_evo"`
	AutoEvoApplyStatus          string `json:"auto_evo_apply_status"`
	AutoEvoGeneration           int64  `json:"auto_evo_generation"`
	AutoEvoError                string `json:"auto_evo_error"`
}

func ListManagedStates(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	memoryRow, err := LoadSystemMemory(r.Context(), db, userID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}
	preferenceRow, err := LoadSystemUserPreference(r.Context(), db, userID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}
	suggestionStatuses, err := LoadManagedSuggestionStatuses(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}

	items := []ManagedStateItem{
		NewManagedStateItem(ResourceTypeMemory, memoryRow, suggestionStatuses[ResourceTypeMemory]),
		NewManagedStateItem(ResourceTypeUserPreference, preferenceRow, suggestionStatuses[ResourceTypeUserPreference]),
	}

	common.ReplyOK(w, map[string]any{"items": items})
}

func LoadSystemMemory(ctx context.Context, db *gorm.DB, userID string) (*orm.SystemMemory, error) {
	var row orm.SystemMemory
	if err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC").
		Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func LoadSystemUserPreference(ctx context.Context, db *gorm.DB, userID string) (*orm.SystemUserPreference, error) {
	var row orm.SystemUserPreference
	if err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC").
		Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func NewManagedStateItem(resourceType string, row any, suggestionStatus string) ManagedStateItem {
	suggestionStatus = CanonicalSuggestionStatus(suggestionStatus)
	item := ManagedStateItem{
		ResourceType:                strings.TrimSpace(resourceType),
		Title:                       ManagedStateTitle(resourceType),
		HasPendingReviewSuggestions: suggestionStatus != SuggestionStatusNone,
		SuggestionStatus:            suggestionStatus,
	}
	switch typed := row.(type) {
	case *orm.SystemMemory:
		if typed != nil {
			item.ResourceID = strings.TrimSpace(typed.ID)
			item.Content = typed.Content
			item.ContentSummary = ManagedStateSummary(typed.Content)
			item.AutoEvo = typed.AutoEvo
			item.AutoEvoApplyStatus = NormalizeAutoEvoApplyStatus(typed.AutoEvoApplyStatus)
			item.AutoEvoGeneration = typed.AutoEvoGeneration
			item.AutoEvoError = typed.AutoEvoError
		}
	case *orm.SystemUserPreference:
		if typed != nil {
			item.ResourceID = strings.TrimSpace(typed.ID)
			item.Content = typed.Content
			item.ContentSummary = ManagedStateSummary(typed.Content)
			item.AutoEvo = typed.AutoEvo
			item.AutoEvoApplyStatus = NormalizeAutoEvoApplyStatus(typed.AutoEvoApplyStatus)
			item.AutoEvoGeneration = typed.AutoEvoGeneration
			item.AutoEvoError = typed.AutoEvoError
		}
	}
	return item
}

func LoadManagedSuggestionStatuses(ctx context.Context, db *gorm.DB, userID string) (map[string]string, error) {
	var rows []struct {
		ResourceType string `gorm:"column:resource_type"`
		Status       string `gorm:"column:status"`
	}
	if err := db.WithContext(ctx).
		Model(&orm.ResourceSuggestion{}).
		Select("resource_type", "status").
		Where("user_id = ? AND status IN ? AND resource_type IN ?",
			strings.TrimSpace(userID),
			VisibleSuggestionStatuses(),
			[]string{ResourceTypeMemory, ResourceTypeUserPreference},
		).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string, len(rows))
	for _, row := range rows {
		resourceType := strings.TrimSpace(row.ResourceType)
		if resourceType == "" {
			continue
		}
		result[resourceType] = MergeSuggestionStatus(result[resourceType], row.Status)
	}
	return result, nil
}

func ManagedSuggestionStatusForResource(ctx context.Context, db *gorm.DB, userID, resourceType string) (string, error) {
	statuses, err := LoadManagedSuggestionStatuses(ctx, db, userID)
	if err != nil {
		return SuggestionStatusNone, err
	}
	return CanonicalSuggestionStatus(statuses[strings.TrimSpace(resourceType)]), nil
}

func ManagedStateTitle(resourceType string) string {
	switch strings.TrimSpace(resourceType) {
	case ResourceTypeMemory:
		return ManagedMemoryTitle
	case ResourceTypeUserPreference:
		return ManagedPreferenceTitle
	default:
		return strings.TrimSpace(resourceType)
	}
}

func ManagedStateSummary(content string) string {
	if fields := strings.Fields(content); len(fields) > 0 {
		return strings.Join(fields, " ")
	}
	return ""
}

func applyManagedMemoryAutoEvolution(ctx context.Context, db *gorm.DB, row orm.SystemMemory) (bool, error) {
	pending, err := LoadAutoApplicableSuggestions(ctx, db, row.UserID, ResourceTypeMemory, SystemResourceKey(ResourceTypeMemory))
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, nil
	}
	generated, genErr := algo.GenerateMemory(ctx, algo.MemoryGenerateRequest{
		Content:     row.Content,
		Suggestions: memoryAlgoSuggestions(pending),
	})
	if genErr != nil {
		return false, genErr
	}
	now := time.Now()
	result := db.WithContext(ctx).Model(&orm.SystemMemory{}).
		Where("id = ? AND version = ? AND auto_evo = ? AND auto_evo_generation = ?",
			row.ID, row.Version, true, row.AutoEvoGeneration).
		Updates(map[string]any{
			"content":               generated,
			"content_hash":          HashContent(generated),
			"version":               row.Version + 1,
			"draft_content":         "",
			"draft_source_version":  0,
			"draft_status":          "",
			"draft_updated_at":      nil,
			"auto_evo_apply_status": AutoEvoApplyStatusRunning,
			"auto_evo_error":        "",
			"updated_at":            now,
			"ext":                   WithDraftSuggestionIDs(row.Ext, nil),
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	if err := UpdateSuggestionStatus(ctx, db, memorySuggestionIDs(pending), SuggestionStatusApplied); err != nil {
		return false, err
	}
	return true, nil
}

func applyManagedPreferenceAutoEvolution(ctx context.Context, db *gorm.DB, row orm.SystemUserPreference) (bool, error) {
	pending, err := LoadAutoApplicableSuggestions(ctx, db, row.UserID, ResourceTypeUserPreference, SystemResourceKey(ResourceTypeUserPreference))
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, nil
	}
	generated, genErr := algo.GenerateUserPreference(ctx, algo.MemoryGenerateRequest{
		Content:     row.Content,
		Suggestions: prefAlgoSuggestions(pending),
	})
	if genErr != nil {
		return false, genErr
	}
	now := time.Now()
	result := db.WithContext(ctx).Model(&orm.SystemUserPreference{}).
		Where("id = ? AND version = ? AND auto_evo = ? AND auto_evo_generation = ?",
			row.ID, row.Version, true, row.AutoEvoGeneration).
		Updates(map[string]any{
			"content":               generated,
			"content_hash":          HashContent(generated),
			"version":               row.Version + 1,
			"draft_content":         "",
			"draft_source_version":  0,
			"draft_status":          "",
			"draft_updated_at":      nil,
			"auto_evo_apply_status": AutoEvoApplyStatusRunning,
			"auto_evo_error":        "",
			"updated_at":            now,
			"ext":                   WithDraftSuggestionIDs(row.Ext, nil),
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	if err := UpdateSuggestionStatus(ctx, db, prefSuggestionIDs(pending), SuggestionStatusApplied); err != nil {
		return false, err
	}
	return true, nil
}

func EnsureManagedMemoryAutoEvolutionScheduled(row orm.SystemMemory) error {
	if !row.AutoEvo {
		return nil
	}
	workerKey := AutoEvoWorkerKey(ResourceTypeMemory, row.ID)
	if !TryAcquireAutoEvoWorker(workerKey) {
		return nil
	}
	db := store.DB()
	if db == nil {
		ReleaseAutoEvoWorker(workerKey)
		return errors.New("store not initialized")
	}
	var latest orm.SystemMemory
	if err := db.WithContext(context.Background()).Where("id = ?", row.ID).Take(&latest).Error; err != nil {
		ReleaseAutoEvoWorker(workerKey)
		return err
	}
	if !latest.AutoEvo {
		ReleaseAutoEvoWorker(workerKey)
		return nil
	}
	pending, err := LoadAutoApplicableSuggestions(context.Background(), db, latest.UserID, ResourceTypeMemory, SystemResourceKey(ResourceTypeMemory))
	if err != nil {
		ReleaseAutoEvoWorker(workerKey)
		return err
	}
	now := time.Now()
	status := AutoEvoApplyStatusRunning
	if len(pending) == 0 {
		status = AutoEvoApplyStatusIdle
	}
	if err := db.WithContext(context.Background()).Model(&orm.SystemMemory{}).Where("id = ?", latest.ID).Updates(map[string]any{
		"auto_evo_apply_status": status,
		"auto_evo_started_at":   map[bool]any{true: now, false: nil}[len(pending) > 0],
		"auto_evo_finished_at":  map[bool]any{true: nil, false: now}[len(pending) > 0],
		"auto_evo_error":        "",
		"updated_at":            now,
	}).Error; err != nil {
		ReleaseAutoEvoWorker(workerKey)
		return err
	}
	if len(pending) == 0 {
		ReleaseAutoEvoWorker(workerKey)
		return nil
	}
	go runManagedMemoryAutoEvolutionLoop(latest.ID, workerKey)
	return nil
}

func EnsureManagedPreferenceAutoEvolutionScheduled(row orm.SystemUserPreference) error {
	if !row.AutoEvo {
		return nil
	}
	workerKey := AutoEvoWorkerKey(ResourceTypeUserPreference, row.ID)
	if !TryAcquireAutoEvoWorker(workerKey) {
		return nil
	}
	db := store.DB()
	if db == nil {
		ReleaseAutoEvoWorker(workerKey)
		return errors.New("store not initialized")
	}
	var latest orm.SystemUserPreference
	if err := db.WithContext(context.Background()).Where("id = ?", row.ID).Take(&latest).Error; err != nil {
		ReleaseAutoEvoWorker(workerKey)
		return err
	}
	if !latest.AutoEvo {
		ReleaseAutoEvoWorker(workerKey)
		return nil
	}
	pending, err := LoadAutoApplicableSuggestions(context.Background(), db, latest.UserID, ResourceTypeUserPreference, SystemResourceKey(ResourceTypeUserPreference))
	if err != nil {
		ReleaseAutoEvoWorker(workerKey)
		return err
	}
	now := time.Now()
	status := AutoEvoApplyStatusRunning
	if len(pending) == 0 {
		status = AutoEvoApplyStatusIdle
	}
	if err := db.WithContext(context.Background()).Model(&orm.SystemUserPreference{}).Where("id = ?", latest.ID).Updates(map[string]any{
		"auto_evo_apply_status": status,
		"auto_evo_started_at":   map[bool]any{true: now, false: nil}[len(pending) > 0],
		"auto_evo_finished_at":  map[bool]any{true: nil, false: now}[len(pending) > 0],
		"auto_evo_error":        "",
		"updated_at":            now,
	}).Error; err != nil {
		ReleaseAutoEvoWorker(workerKey)
		return err
	}
	if len(pending) == 0 {
		ReleaseAutoEvoWorker(workerKey)
		return nil
	}
	go runManagedPreferenceAutoEvolutionLoop(latest.ID, workerKey)
	return nil
}

func runManagedMemoryAutoEvolutionLoop(memoryID, workerKey string) {
	defer ReleaseAutoEvoWorker(workerKey)
	ctx := context.Background()
	db := store.DB()
	if db == nil {
		return
	}
	for {
		var row orm.SystemMemory
		if err := db.WithContext(ctx).Where("id = ?", memoryID).Take(&row).Error; err != nil {
			return
		}
		if !row.AutoEvo {
			return
		}
		pending, err := LoadAutoApplicableSuggestions(ctx, db, row.UserID, ResourceTypeMemory, SystemResourceKey(ResourceTypeMemory))
		if err != nil {
			_ = db.WithContext(ctx).Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": AutoEvoApplyStatusFailed,
				"auto_evo_error":        err.Error(),
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		if len(pending) == 0 {
			_ = db.WithContext(ctx).Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": AutoEvoApplyStatusIdle,
				"auto_evo_error":        "",
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		generation := row.AutoEvoGeneration
		applied, err := applyManagedMemoryAutoEvolution(ctx, db, row)
		if err != nil {
			_ = db.WithContext(ctx).Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": AutoEvoApplyStatusFailed,
				"auto_evo_error":        err.Error(),
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		if !applied {
			var latest orm.SystemMemory
			if err := db.WithContext(ctx).Where("id = ?", row.ID).Take(&latest).Error; err != nil {
				return
			}
			if !latest.AutoEvo {
				return
			}
			if latest.AutoEvoGeneration != generation {
				continue
			}
		}
	}
}

func runManagedPreferenceAutoEvolutionLoop(preferenceID, workerKey string) {
	defer ReleaseAutoEvoWorker(workerKey)
	ctx := context.Background()
	db := store.DB()
	if db == nil {
		return
	}
	for {
		var row orm.SystemUserPreference
		if err := db.WithContext(ctx).Where("id = ?", preferenceID).Take(&row).Error; err != nil {
			return
		}
		if !row.AutoEvo {
			return
		}
		pending, err := LoadAutoApplicableSuggestions(ctx, db, row.UserID, ResourceTypeUserPreference, SystemResourceKey(ResourceTypeUserPreference))
		if err != nil {
			_ = db.WithContext(ctx).Model(&orm.SystemUserPreference{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": AutoEvoApplyStatusFailed,
				"auto_evo_error":        err.Error(),
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		if len(pending) == 0 {
			_ = db.WithContext(ctx).Model(&orm.SystemUserPreference{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": AutoEvoApplyStatusIdle,
				"auto_evo_error":        "",
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		generation := row.AutoEvoGeneration
		applied, err := applyManagedPreferenceAutoEvolution(ctx, db, row)
		if err != nil {
			_ = db.WithContext(ctx).Model(&orm.SystemUserPreference{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": AutoEvoApplyStatusFailed,
				"auto_evo_error":        err.Error(),
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		if !applied {
			var latest orm.SystemUserPreference
			if err := db.WithContext(ctx).Where("id = ?", row.ID).Take(&latest).Error; err != nil {
				return
			}
			if !latest.AutoEvo {
				return
			}
			if latest.AutoEvoGeneration != generation {
				continue
			}
		}
	}
}

func memorySuggestionIDs(rows []orm.ResourceSuggestion) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if trimmed := strings.TrimSpace(row.ID); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}

func prefSuggestionIDs(rows []orm.ResourceSuggestion) []string {
	return memorySuggestionIDs(rows)
}

func memoryAlgoSuggestions(rows []orm.ResourceSuggestion) []algo.Suggestion {
	result := make([]algo.Suggestion, 0, len(rows))
	for _, row := range rows {
		result = append(result, algo.Suggestion{
			Title:   strings.TrimSpace(row.Title),
			Content: strings.TrimSpace(row.Content),
			Reason:  strings.TrimSpace(row.Reason),
		})
	}
	return result
}

func prefAlgoSuggestions(rows []orm.ResourceSuggestion) []algo.Suggestion {
	return memoryAlgoSuggestions(rows)
}
