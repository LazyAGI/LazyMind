package chat

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

const searchProviderCategory = "search"
const datasourceProviderCategory = "datasource"

type selectedToolProviderCredential struct {
	ProviderName string
	APIKey       string
}

func loadSelectedSearchToolCredential(ctx context.Context, db *gorm.DB, userID string) (*selectedToolProviderCredential, error) {
	userID = strings.TrimSpace(userID)
	if db == nil || userID == "" {
		return nil, nil
	}
	if item, err := loadSelectedSearchToolCredentialForUser(ctx, db, userID); err != nil || item != nil {
		return item, err
	}
	return loadSharedToolCredential(ctx, db, searchProviderCategory)
}

func loadAcademicSearchToolCredential(ctx context.Context, db *gorm.DB, userID string) (*selectedToolProviderCredential, error) {
	if item, err := loadSelectedToolCredentialForUser(ctx, db, userID, datasourceProviderCategory); err != nil || item != nil {
		return item, err
	}
	if item, err := loadConfiguredSciverseDatasourceCredentialForUser(ctx, db, userID); err != nil || item != nil {
		return item, err
	}
	return loadSharedToolCredential(ctx, db, datasourceProviderCategory)
}

func loadSelectedSearchToolCredentialForUser(
	ctx context.Context,
	db *gorm.DB,
	userID string,
) (*selectedToolProviderCredential, error) {
	return scanSelectedSearchToolCredential(
		db.WithContext(ctx).Table("user_selected_providers usp").
			Where("usp.user_id = ? AND usp.category = ?", userID, searchProviderCategory),
	)
}

func loadSelectedToolCredentialForUser(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	category string,
) (*selectedToolProviderCredential, error) {
	return scanSelectedSearchToolCredential(
		db.WithContext(ctx).Table("user_selected_providers usp").
			Where("usp.user_id = ? AND usp.category = ?", userID, category),
	)
}

func loadSharedToolCredential(ctx context.Context, db *gorm.DB, category string) (*selectedToolProviderCredential, error) {
	return scanSelectedSearchToolCredential(
		db.WithContext(ctx).Table("user_selected_providers usp").
			Where("usp.share = ? AND usp.category = ?", true, category),
	)
}

func loadConfiguredSciverseDatasourceCredentialForUser(
	ctx context.Context,
	db *gorm.DB,
	userID string,
) (*selectedToolProviderCredential, error) {
	var row struct {
		ProviderName string `gorm:"column:provider_name"`
		APIKey       string `gorm:"column:api_key"`
	}
	err := db.WithContext(ctx).Table("user_model_provider_groups g").
		Select("p.name AS provider_name, g.api_key").
		Joins(
			"JOIN user_model_providers p ON "+
				"p.id = g.user_model_provider_id AND "+
				"p.create_user_id = g.create_user_id AND "+
				"p.deleted_at IS NULL",
		).
		Where(
			"g.create_user_id = ? AND g.deleted_at IS NULL AND g.is_verified = ? AND TRIM(g.api_key) <> '' AND p.category = ? AND p.name IN ?",
			userID,
			true,
			datasourceProviderCategory,
			[]string{"Sciverse", "Sciverse Search"},
		).
		Order("g.updated_at DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.ProviderName) == "" || strings.TrimSpace(row.APIKey) == "" {
		return nil, nil
	}
	return &selectedToolProviderCredential{
		ProviderName: row.ProviderName,
		APIKey:       row.APIKey,
	}, nil
}

func scanSelectedSearchToolCredential(q *gorm.DB) (*selectedToolProviderCredential, error) {
	var row struct {
		ProviderName string `gorm:"column:provider_name"`
		APIKey       string `gorm:"column:api_key"`
	}
	err := q.Select("p.name AS provider_name, g.api_key").
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = usp.user_model_provider_group_id AND "+
				"g.create_user_id = usp.user_id AND "+
				"g.deleted_at IS NULL AND "+
				"g.is_verified = ?",
			true,
		).
		Joins(
			"JOIN user_model_providers p ON "+
				"p.id = g.user_model_provider_id AND "+
				"p.create_user_id = usp.user_id AND "+
				"p.deleted_at IS NULL",
		).
		Where(
			"(usp.category = ? AND p.category = ?) OR (usp.category = ? AND p.category = ? AND p.name IN ?)",
			searchProviderCategory,
			searchProviderCategory,
			datasourceProviderCategory,
			datasourceProviderCategory,
			[]string{"Sciverse", "Sciverse Search"},
		).
		Order("usp.updated_at DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.ProviderName) == "" || strings.TrimSpace(row.APIKey) == "" {
		return nil, nil
	}
	return &selectedToolProviderCredential{
		ProviderName: row.ProviderName,
		APIKey:       row.APIKey,
	}, nil
}

func searchToolConfigName(providerName string) string {
	switch normalizeToolProviderName(providerName) {
	case "google", "googlesearch", "googlecustomsearch":
		return "google"
	case "bocha", "bochasearch":
		return "bocha"
	case "sciverse", "sciversesearch":
		return "sciverse"
	case "bing", "bingsearch":
		return "bing"
	case "tavily":
		return "tavily"
	default:
		return ""
	}
}

func normalizeToolProviderName(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return -1
	}, value)
}

func mergeToolConfig(base map[string]any, entries ...map[string]any) map[string]any {
	out := base
	for _, entry := range entries {
		for key, value := range entry {
			key = strings.TrimSpace(key)
			normalizedValue := normalizeToolConfigValue(value)
			if key == "" || normalizedValue == nil {
				continue
			}
			if out == nil {
				out = map[string]any{}
			}
			out[key] = normalizedValue
		}
	}
	return out
}

func normalizeToolConfigValue(value any) any {
	if s, ok := value.(string); ok {
		keys := splitToolConfigKeys(s)
		if len(keys) == 0 {
			return nil
		}
		if len(keys) == 1 {
			return keys[0]
		}
		return keys
	}
	if values, ok := value.([]string); ok {
		keys := make([]string, 0, len(values))
		for _, item := range values {
			item = strings.TrimSpace(item)
			if item != "" {
				keys = append(keys, item)
			}
		}
		if len(keys) == 0 {
			return nil
		}
		if len(keys) == 1 {
			return keys[0]
		}
		return keys
	}
	return nil
}

func splitToolConfigKeys(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func searchToolConfigEntry(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	credential, err := loadSelectedSearchToolCredential(ctx, db, userID)
	return toolConfigEntryFromCredential(credential, err)
}

func academicSearchToolConfigEntry(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	credential, err := loadAcademicSearchToolCredential(ctx, db, userID)
	return toolConfigEntryFromCredential(credential, err)
}

func toolConfigEntryFromCredential(
	credential *selectedToolProviderCredential,
	err error,
) (map[string]any, error) {
	if err != nil || credential == nil {
		return nil, err
	}
	toolName := searchToolConfigName(credential.ProviderName)
	if toolName == "" {
		return nil, nil
	}
	value := normalizeToolConfigValue(credential.APIKey)
	if value == nil {
		return nil, nil
	}
	return map[string]any{toolName: value}, nil
}
