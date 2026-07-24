package modelconfig

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

type SelectedRuntimeModel struct {
	ModelType    string
	ProviderName string
	ModelName    string
	BaseURL      string
	APIKey       string
}

func LoadLLMConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	// Step 1: load the user's own selections.
	var ownRows []SelectedRuntimeModel
	err := db.WithContext(ctx).
		Table("user_selected_models usm").
		Select(
			"usm.model_type, "+
				"m.provider_name, "+
				"m.name AS model_name, "+
				"g.base_url, "+
				"g.api_key",
		).
		Joins(
			"JOIN user_model_provider_group_models m ON "+
				"m.id = usm.user_model_provider_group_model_id AND "+
				"m.create_user_id = usm.user_id AND "+
				"m.deleted_at IS NULL",
		).
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND "+
				"g.create_user_id = usm.user_id AND "+
				"g.deleted_at IS NULL",
		).
		Where("usm.user_id = ?", strings.TrimSpace(userID)).
		Scan(&ownRows).Error
	if err != nil {
		return nil, err
	}

	// Collect which model_types the user already has.
	coveredTypes := make(map[string]struct{}, len(ownRows))
	for _, row := range ownRows {
		coveredTypes[strings.ToLower(strings.TrimSpace(row.ModelType))] = struct{}{}
	}

	// Step 2: for model_types not covered by the user, fall back to share=true rows.
	var sharedRows []SelectedRuntimeModel
	err = db.WithContext(ctx).
		Table("user_selected_models usm").
		Select(
			"usm.model_type, "+
				"m.provider_name, "+
				"m.name AS model_name, "+
				"g.base_url, "+
				"g.api_key",
		).
		Joins(
			"JOIN user_model_provider_group_models m ON "+
				"m.id = usm.user_model_provider_group_model_id AND "+
				"m.deleted_at IS NULL",
		).
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND "+
				"g.deleted_at IS NULL",
		).
		Where("usm.share = ?", true).
		Scan(&sharedRows).Error
	if err != nil {
		return nil, err
	}

	// Merge: own rows take priority; shared rows fill in missing types.
	rows := make([]SelectedRuntimeModel, 0, len(ownRows)+len(sharedRows))
	rows = append(rows, ownRows...)
	for _, row := range sharedRows {
		normalized := strings.ToLower(strings.TrimSpace(row.ModelType))
		if _, covered := coveredTypes[normalized]; !covered {
			rows = append(rows, row)
			coveredTypes[normalized] = struct{}{}
		}
	}

	return BuildLLMConfig(rows), nil
}

func LoadOCRConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	row, err := loadSelectedProviderConfig(ctx, db, strings.TrimSpace(userID), "ocr", false)
	if err != nil {
		return nil, err
	}
	if row == nil {
		row, err = loadSelectedProviderConfig(ctx, db, "", "ocr", true)
		if err != nil {
			return nil, err
		}
	}
	if row == nil {
		return nil, nil
	}
	ocrType := normalizeOCRType(row.ProviderName)
	if ocrType == "" {
		return nil, nil
	}
	config := map[string]any{
		"ocr_type": ocrType,
		"ocr_url":  row.BaseURL,
	}
	if authValue := normalizeOCRAuthValue(row.APIKey); authValue != nil {
		config["ocr_auth"] = map[string]any{ocrType: authValue}
	}
	return config, nil
}

func normalizeOCRAuthValue(raw string) any {
	keys := splitOCRAuthKeys(raw)
	if len(keys) == 0 {
		return nil
	}
	if len(keys) == 1 {
		return keys[0]
	}
	return keys
}

func splitOCRAuthKeys(raw string) []string {
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

type selectedProviderConfig struct {
	ProviderName string
	BaseURL      string
	APIKey       string
}

func loadSelectedProviderConfig(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	category string,
	sharedOnly bool,
) (*selectedProviderConfig, error) {
	var row selectedProviderConfig
	q := db.WithContext(ctx).Table("user_selected_providers usp").
		Select(
			"p.name AS provider_name, "+
				"g.base_url, "+
				"g.api_key",
		).
		Joins("JOIN user_model_provider_groups g ON g.id = usp.user_model_provider_group_id AND g.deleted_at IS NULL").
		Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.deleted_at IS NULL").
		Where("usp.category = ?", category)
	if sharedOnly {
		q = q.Where("usp.share = ?", true)
	} else {
		q = q.Where("usp.user_id = ?", userID)
	}
	err := q.Order("usp.updated_at DESC").Limit(1).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ProviderName == "" && row.BaseURL == "" {
		return nil, nil
	}
	return &row, nil
}

func normalizeOCRType(providerName string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(providerName), " ", "")) {
	case "mineru":
		return "mineru"
	case "paddleocr", "paddle":
		return "paddleocr"
	default:
		return ""
	}
}

// LoadAdminEmbedConfig queries the first system-wide default embedding model
// (is_default=true, model_type=embed_main) across all users, and returns it as
// an embed_main config map. This is the admin-configured embedding model shared
// by all users for document parsing and knowledge-base search.
// Returns nil when no default embedding model is configured.
func LoadAdminEmbedConfig(ctx context.Context, db *gorm.DB) (map[string]any, error) {
	var row SelectedRuntimeModel
	err := db.WithContext(ctx).
		Table("user_model_provider_group_models m").
		Select("m.provider_name, m.name AS model_name, g.base_url, g.api_key").
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND "+
				"g.deleted_at IS NULL",
		).
		Where("m.model_type IN ? AND m.is_default = ? AND m.deleted_at IS NULL", []string{"embed", "cross_modal_embed"}, true).
		Order("m.created_at ASC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ProviderName == "" && row.ModelName == "" {
		return nil, nil
	}
	cfg := map[string]any{
		"source":   strings.ToLower(strings.TrimSpace(row.ProviderName)),
		"model":    row.ModelName,
		"base_url": row.BaseURL,
		"api_key":  row.APIKey,
	}
	return cfg, nil
}

func BuildLLMConfig(rows []SelectedRuntimeModel) map[string]any {
	out := map[string]any{}
	for _, row := range rows {
		cfg := map[string]any{
			"source":   strings.ToLower(strings.TrimSpace(row.ProviderName)),
			"model":    row.ModelName,
			"base_url": row.BaseURL,
			"api_key":  row.APIKey,
		}
		out[strings.ToLower(strings.TrimSpace(row.ModelType))] = cfg
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func SummarizeLLMConfigForLog(config map[string]any) string {
	if len(config) == 0 {
		return "roles=[]"
	}
	roles := make([]string, 0, len(config))
	for role := range config {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	parts := make([]string, 0, len(roles)+1)
	parts = append(parts, "roles=["+strings.Join(roles, ",")+"]")
	for _, role := range roles {
		roleConfig, _ := config[role].(map[string]any)
		if roleConfig == nil {
			parts = append(parts, fmt.Sprintf("%s(type=%T)", role, config[role]))
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s(source=%s, model=%s, base_url=%s, api_key=%s)",
			role,
			stringValue(roleConfig["source"]),
			stringValue(roleConfig["model"]),
			stringValue(roleConfig["base_url"]),
			APIKeyState(roleConfig["api_key"]),
		))
	}
	return strings.Join(parts, " ")
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
}

func APIKeyState(value any) string {
	if strings.TrimSpace(stringValue(value)) == "" {
		return "empty"
	}
	return "set"
}
