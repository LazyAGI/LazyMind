package modelconfig

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// LoadOwnedToolModels resolves only the explicitly requested model roles for an
// external tool. Discovery and deterministic tools pass no roles and perform no
// model lookup. Shared selections and runtime-wide fallback are deliberately not
// inherited by an external caller; missing capabilities remain configurable.
func LoadOwnedToolModels(ctx context.Context, db *gorm.DB, userID string, roles []string) (map[string]any, error) {
	wanted := make(map[string]bool, len(roles))
	keys := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role != "" && !wanted[role] {
			wanted[role] = true
			keys = append(keys, role)
		}
	}
	if len(keys) == 0 {
		return map[string]any{}, nil
	}
	rows := []SelectedRuntimeModel{}
	cloudActive := map[string]bool{}
	if db.Migrator().HasTable(&orm.UserSelectedCloudModel{}) {
		var selections []orm.UserSelectedCloudModel
		if err := db.WithContext(ctx).Where("user_id = ? AND LOWER(TRIM(model_type)) IN ?", strings.TrimSpace(userID), keys).Find(&selections).Error; err != nil {
			return nil, err
		}
		for _, selection := range selections {
			row, available, err := ResolveCloudRuntimeModel(ctx, selection.ModelType, selection.PublicModelKey)
			if err != nil || !available {
				continue // An unavailable Cloud connection may use this owner's local selection.
			}
			rows = append(rows, row)
			cloudActive[strings.ToLower(strings.TrimSpace(selection.ModelType))] = true
		}
	}
	var local []SelectedRuntimeModel
	err := db.WithContext(ctx).Table("user_selected_models usm").
		Select("usm.model_type, m.model_type AS technical_model_type, m.vision, m.model_settings, m.is_default, m.provider_name, m.name AS model_name, m.user_model_provider_id AS runtime_provider_id, g.id AS runtime_group_id, g.id AS provider_group_id, g.credential_revision, g.base_url, g.api_key, g.api_key_ciphertext, m.max_input_tokens, m.input_token_limit, m.max_output_tokens").
		Joins("JOIN user_model_provider_group_models m ON m.id = usm.user_model_provider_group_model_id AND m.create_user_id = usm.user_id AND m.deleted_at IS NULL").
		Joins("JOIN user_model_provider_groups g ON g.id = m.user_model_provider_group_id AND g.create_user_id = usm.user_id AND g.user_model_provider_id = m.user_model_provider_id AND g.deleted_at IS NULL AND g.is_verified = ?", true).
		Where("usm.user_id = ? AND LOWER(TRIM(usm.model_type)) IN ?", strings.TrimSpace(userID), keys).Scan(&local).Error
	if err != nil {
		return nil, err
	}
	populateRuntimeIdentities(ctx, db, local)
	local = eligibleRuntimeModels(local)
	for _, row := range local {
		if cloudActive[strings.ToLower(strings.TrimSpace(row.ModelType))] {
			continue
		}
		selected := []SelectedRuntimeModel{row}
		if err := decryptRuntimeModels(ctx, db, selected); err != nil {
			return nil, err
		}
		rows = append(rows, selected[0])
	}
	config := BuildLLMConfig(rows)
	if config == nil {
		config = map[string]any{}
	}
	return config, nil
}
