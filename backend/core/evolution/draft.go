package evolution

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func DraftSuggestionIDs(ext json.RawMessage) []string {
	var payload map[string]any
	if len(ext) > 0 && json.Unmarshal(ext, &payload) == nil {
		raw, _ := payload["draft_suggestion_ids"].([]any)
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func WithDraftSuggestionIDs(ext json.RawMessage, ids []string) json.RawMessage {
	payload := map[string]any{}
	if len(ext) > 0 {
		_ = json.Unmarshal(ext, &payload)
	}
	if len(ids) == 0 {
		delete(payload, "draft_suggestion_ids")
	} else {
		payload["draft_suggestion_ids"] = ids
	}
	if len(payload) == 0 {
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ext
	}
	return b
}

func LoadApprovedSuggestions(ctx context.Context, db *gorm.DB, userID, resourceType, resourceKey string, ids []string) ([]orm.ResourceSuggestion, error) {
	query := db.WithContext(ctx).Model(&orm.ResourceSuggestion{}).
		Where("resource_type = ? AND resource_key = ? AND status IN ?", strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey), AcceptedSuggestionStatuses())
	if userID = strings.TrimSpace(userID); userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	}
	var rows []orm.ResourceSuggestion
	if err := query.Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func AutoApplicableSuggestionStatuses() []string {
	return []string{SuggestionStatusPendingReview, SuggestionStatusAccepted}
}

func LoadAutoApplicableSuggestions(ctx context.Context, db *gorm.DB, userID, resourceType, resourceKey string) ([]orm.ResourceSuggestion, error) {
	var rows []orm.ResourceSuggestion
	if err := db.WithContext(ctx).Model(&orm.ResourceSuggestion{}).
		Where("user_id = ? AND resource_type = ? AND resource_key = ? AND status IN ?",
			strings.TrimSpace(userID),
			strings.TrimSpace(resourceType),
			strings.TrimSpace(resourceKey),
			AutoApplicableSuggestionStatuses()).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func LoadPendingReviewSuggestions(ctx context.Context, db *gorm.DB, userID, resourceType, resourceKey string) ([]orm.ResourceSuggestion, error) {
	var rows []orm.ResourceSuggestion
	if err := db.WithContext(ctx).Model(&orm.ResourceSuggestion{}).
		Where("user_id = ? AND resource_type = ? AND resource_key = ? AND status = ?",
			strings.TrimSpace(userID),
			strings.TrimSpace(resourceType),
			strings.TrimSpace(resourceKey),
			SuggestionStatusPendingReview).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func UpdateSuggestionStatus(ctx context.Context, db *gorm.DB, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return db.WithContext(ctx).Model(&orm.ResourceSuggestion{}).
		Where("id IN ?", ids).
		Updates(map[string]any{
			"status":     strings.TrimSpace(status),
			"updated_at": now,
		}).Error
}
