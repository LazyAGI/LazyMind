package modelprovider

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// ConfiguredProviderCounts distinguishes supplier inventory from external service readiness.
type ConfiguredProviderCounts struct {
	ModelProviders   int64
	ExternalServices int64
}

// CountConfiguredProviders matches the unfiltered provider workspace navigation
// for models and the configured service cards for other categories. Selected
// model roles, browser-local hiding and search do not alter this inventory.
func CountConfiguredProviders(ctx context.Context, db *gorm.DB, userID string) (ConfiguredProviderCounts, error) {
	var rows []orm.UserModelProvider
	if err := db.WithContext(ctx).Where("create_user_id = ? AND deleted_at IS NULL", userID).Find(&rows).Error; err != nil {
		return ConfiguredProviderCounts{}, err
	}
	var services []orm.UserModelProvider
	for _, row := range rows {
		if row.Category != defaultProviderCategory {
			services = append(services, row)
		}
	}
	configuredServices, err := configuredProviderIDs(ctx, db, services)
	if err != nil {
		return ConfiguredProviderCounts{}, err
	}

	// ProviderWorkspace.hasProviderConfiguration accepts saved credentials,
	// saved visible models, or a verified keyless connection. This is inventory,
	// not the stricter service-card readiness check above. Match ListGroups by
	// excluding default models of an unverified custom supplier.
	var configuredModelIDs []string
	err = db.WithContext(ctx).Table("user_model_provider_groups g").
		Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id").
		Where("p.create_user_id = ? AND p.deleted_at IS NULL AND p.category = ? AND g.create_user_id = ? AND g.deleted_at IS NULL", userID, defaultProviderCategory, userID).
		Where(`TRIM(g.api_key) <> '' OR TRIM(g.api_key_ciphertext) <> '' OR g.is_verified = ? OR EXISTS (
            SELECT 1 FROM user_model_provider_group_models m
            WHERE m.user_model_provider_group_id = g.id AND m.user_model_provider_id = p.id
                AND m.create_user_id = ? AND m.deleted_at IS NULL
                AND (p.is_custom = ? OR g.is_verified = ? OR m.is_default = ?)
        )`, true, userID, false, true, false).
		Distinct("g.user_model_provider_id").Pluck("g.user_model_provider_id", &configuredModelIDs).Error
	if err != nil {
		return ConfiguredProviderCounts{}, err
	}
	configuredModels := make(map[string]bool, len(configuredModelIDs))
	for _, id := range configuredModelIDs {
		configuredModels[id] = true
	}
	navigationNodes := make(map[string]bool)
	for _, row := range rows {
		if !configuredModels[row.ID] {
			continue
		}
		key := row.ID
		if brand, service := ProviderServiceIdentity(row); brand != "" && service != "" {
			key = "brand:" + brand
		}
		navigationNodes[key] = true
	}
	return ConfiguredProviderCounts{ModelProviders: int64(len(navigationNodes)), ExternalServices: int64(len(configuredServices))}, nil
}

func isProviderListed(row orm.UserModelProvider) bool {
	return row.Category != "translation" || IsTranslationProviderAvailable(row.Name)
}

func configuredProviderIDs(ctx context.Context, db *gorm.DB, rows []orm.UserModelProvider) (map[string]bool, error) {
	configured := make(map[string]bool)
	if len(rows) == 0 {
		return configured, nil
	}
	ids := make([]string, 0, len(rows))
	providers := make(map[string]orm.UserModelProvider, len(rows))
	for _, row := range rows {
		if !isProviderListed(row) {
			continue
		}
		ids = append(ids, row.ID)
		providers[row.ID] = row
	}
	if len(ids) == 0 {
		return configured, nil
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
