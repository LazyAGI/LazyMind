package modelprovider

import (
	"lazymind/core/common/orm"
	"lazymind/core/modelsettings"
)

type ModelSettings = modelsettings.Settings

func modelSettingsResponse(raw string) *ModelSettings {
	value, err := modelsettings.Decode(raw)
	if err != nil || value.FeatureSupport == nil && value.FeatureEnabled == nil && value.Embedding == nil {
		return nil
	}
	return &value
}

func normalizeSavedModelSettings(row *orm.UserModelProviderGroupModel, value *ModelSettings) error {
	normalized, err := modelsettings.Normalize(row.ModelType, value)
	if err != nil {
		return err
	}
	row.ModelSettings = normalized
	settings, _ := modelsettings.Decode(normalized)
	features := modelsettings.Effective(settings, row.Vision, row.ModelType)
	if !isChatTechnicalType(row.ModelType) {
		row.Vision = false
	} else if value != nil && (value.FeatureSupport != nil && value.FeatureSupport.Vision != nil || value.FeatureEnabled != nil && value.FeatureEnabled.Vision != nil) {
		row.Vision = features.Vision != nil && *features.Vision
	}
	return nil
}

func EffectiveModelFeatures(row orm.UserModelProviderGroupModel) modelsettings.FeatureFlags {
	settings, err := modelsettings.Decode(row.ModelSettings)
	if err != nil {
		no := false
		return modelsettings.FeatureFlags{Vision: &no, ToolCalling: &no, Reasoning: &no}
	}
	return modelsettings.Effective(settings, row.Vision, row.ModelType)
}
