package modelprovider

import (
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/modelsettings"
)

// Existing copied templates may acquire missing source declarations, while
// explicit declarations and activation choices remain the user's configuration.
func inheritCatalogModelSupport(tx *gorm.DB, providerID, name, modelType, sourceJSON string) error {
	source, err := modelsettings.Decode(sourceJSON)
	if err != nil {
		return err
	}
	providerIDs := tx.Model(&orm.UserModelProvider{}).Select("id").Where("default_model_provider_id = ? AND deleted_at IS NULL", providerID)
	var rows []orm.UserModelProviderGroupModel
	if err := tx.Where("is_default = ? AND name = ? AND model_type = ? AND user_model_provider_id IN (?) AND deleted_at IS NULL", true, name, modelType, providerIDs).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		settings, err := modelsettings.Decode(row.ModelSettings)
		if err != nil {
			continue
		}
		if source.FeatureSupport != nil {
			inheritMissingModelFeatureSupport(&settings, source.FeatureSupport)
		}
		if err := normalizeSavedModelSettings(&row, &settings); err != nil {
			continue
		}
		if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", row.ID).Updates(map[string]any{"model_settings": row.ModelSettings, "vision": row.Vision}).Error; err != nil {
			return err
		}
	}
	return nil
}

func inheritMissingModelFeatureSupport(settings *ModelSettings, source *modelsettings.FeatureFlags) {
	if source == nil {
		return
	}
	if settings.FeatureSupport == nil {
		settings.FeatureSupport = &modelsettings.FeatureFlags{}
	}
	enabled := modelsettings.FeatureFlags{}
	if settings.FeatureEnabled != nil {
		enabled = *settings.FeatureEnabled
	}
	for _, pair := range []struct {
		target          **bool
		source, enabled *bool
	}{
		{&settings.FeatureSupport.Vision, source.Vision, enabled.Vision},
		{&settings.FeatureSupport.ToolCalling, source.ToolCalling, enabled.ToolCalling},
		{&settings.FeatureSupport.Reasoning, source.Reasoning, enabled.Reasoning},
	} {
		if *pair.target == nil && pair.source != nil && !(!*pair.source && pair.enabled != nil && *pair.enabled) {
			*pair.target = pair.source
		}
	}
}
