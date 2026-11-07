package modelconfig

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/modelprovider"
)

// loadGlobalEmbeddingRuntime resolves the deployment-wide text embedding
// selection. Once the state row exists, stale personal preferences must never
// supply embed_main, even if the active model has become unavailable.
func loadGlobalEmbeddingRuntime(ctx context.Context, db *gorm.DB, credentials runtimeCredentials) (bool, *SelectedRuntimeModel, error) {
	choice, present, err := modelprovider.ResolveGlobalEmbeddingChoice(ctx, db)
	if err != nil {
		return false, nil, err
	}
	if !present {
		return false, nil, nil
	}
	if choice.ModelID == "" || choice.Phase == "clearing" || choice.Phase == "cleanup_failed" || choice.Phase == "unverified" {
		return true, nil, nil
	}
	if choice.Source == "cloud" {
		row, available, err := ResolveCloudRuntimeModel(ctx, "embed_main", choice.ModelID)
		if err != nil || !available {
			return true, nil, nil
		}
		return true, &row, nil
	}
	if choice.Source != "own" {
		return true, nil, nil
	}
	var row SelectedRuntimeModel
	err = db.WithContext(ctx).
		Table("user_model_provider_group_models m").
		Select("'embed_main' AS model_type, m.model_type AS technical_model_type, m.vision, m.model_settings, m.is_default, "+
			"m.provider_name, m.name AS model_name, m.user_model_provider_id AS runtime_provider_id, "+
			"g.id AS runtime_group_id, g.id AS provider_group_id, g.credential_revision, g.base_url, "+
			"g.api_key, g.api_key_ciphertext, m.max_input_tokens, m.input_token_limit, m.max_output_tokens").
		Joins("JOIN user_model_provider_groups g ON g.id = m.user_model_provider_group_id AND g.user_model_provider_id = m.user_model_provider_id AND g.create_user_id = m.create_user_id AND g.deleted_at IS NULL").
		Joins("JOIN user_model_providers p ON p.id = m.user_model_provider_id AND p.create_user_id = m.create_user_id AND p.deleted_at IS NULL").
		Where("m.id = ? AND m.deleted_at IS NULL AND m.enabled = ?", choice.ModelID, true).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil, nil
	}
	if err != nil {
		return true, nil, err
	}
	rows := []SelectedRuntimeModel{row}
	populateRuntimeIdentities(ctx, db, rows)
	if len(eligibleRuntimeModels(rows)) == 0 {
		return true, nil, nil
	}
	if err := decryptRuntimeModelsWithCache(ctx, db, rows, credentials); err != nil {
		// The global vector role is unavailable until its credential is
		// repaired. Other roles, such as chat, must still load normally.
		return true, nil, nil
	}
	if strings.TrimSpace(rows[0].ModelName) == "" {
		return true, nil, nil
	}
	return true, &rows[0], nil
}
