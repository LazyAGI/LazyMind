package modelprovider

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// CountConfiguredServices uses the same readiness rules as the external service
// cards. A service counts once even when it has multiple verified groups.
func CountConfiguredServices(ctx context.Context, db *gorm.DB, userID string) (int64, error) {
	var rows []orm.UserModelProvider
	if err := db.WithContext(ctx).Where("create_user_id = ? AND deleted_at IS NULL AND category <> ?", userID, defaultProviderCategory).Find(&rows).Error; err != nil {
		return 0, err
	}
	configured, err := configuredProviderIDs(ctx, db, rows)
	if err != nil {
		return 0, err
	}
	var count int64
	for _, row := range rows {
		if row.Category == "translation" && !IsTranslationProviderAvailable(row.Name) {
			continue
		}
		if configured[row.ID] {
			count++
		}
	}
	return count, nil
}

func configuredProviderIDs(ctx context.Context, db *gorm.DB, rows []orm.UserModelProvider) (map[string]bool, error) {
	configured := make(map[string]bool)
	if len(rows) == 0 {
		return configured, nil
	}
	ids := make([]string, 0, len(rows))
	providers := make(map[string]orm.UserModelProvider, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		providers[row.ID] = row
	}
	var groups []orm.UserModelProviderGroup
	if err := db.WithContext(ctx).Where("user_model_provider_id IN ? AND deleted_at IS NULL AND is_verified = ?", ids, true).Find(&groups).Error; err != nil {
		return nil, err
	}
	for _, group := range groups {
		parent := providers[group.UserModelProviderID]
		apiKey, _ := apiKeyForGroup(db.WithContext(ctx), &group)
		if strings.TrimSpace(apiKey) != "" ||
			!providerAPIKeyRequiredForBaseURL(ctx, db, parent, group.BaseURL) ||
			(parent.Category == "translation" &&
				!isCustomBaseURL(ctx, db, parent.DefaultModelProviderID, group.BaseURL) &&
				isSelectableProviderGroup(ctx, db, parent.DefaultModelProviderID, group.BaseURL, apiKey)) {
			configured[group.UserModelProviderID] = true
		}
	}
	return configured, nil
}
