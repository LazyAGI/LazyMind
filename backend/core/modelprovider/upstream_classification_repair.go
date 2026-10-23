package modelprovider

import (
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Only catalog-owned rows are repaired. A custom or explicitly edited row has
// is_default=false, including older imports whose origin cannot be established.
// Keep its UUID and metadata rather than guessing at the user's intent.
func reconcileCatalogModelSelections(tx *gorm.DB, providerID string) error {
	if !tx.Migrator().HasTable(&orm.UserSelectedModel{}) {
		return nil
	}
	var parents []orm.UserModelProvider
	if err := tx.Where("default_model_provider_id = ? AND deleted_at IS NULL", providerID).Find(&parents).Error; err != nil {
		return err
	}
	parentByID := make(map[string]orm.UserModelProvider, len(parents))
	parentIDs := make([]string, 0, len(parents))
	for _, parent := range parents {
		parentByID[parent.ID] = parent
		parentIDs = append(parentIDs, parent.ID)
	}
	if len(parentIDs) == 0 {
		return nil
	}
	var models []orm.UserModelProviderGroupModel
	if err := tx.Where("user_model_provider_id IN ? AND is_default = ? AND deleted_at IS NULL", parentIDs, true).Find(&models).Error; err != nil {
		return err
	}
	modelByID := make(map[string]orm.UserModelProviderGroupModel, len(models))
	modelIDs := make([]string, 0, len(models))
	for _, model := range models {
		modelByID[model.ID] = model
		modelIDs = append(modelIDs, model.ID)
	}
	if len(modelIDs) == 0 {
		return nil
	}
	var selections []orm.UserSelectedModel
	if err := tx.Where("user_model_provider_group_model_id IN ?", modelIDs).Find(&selections).Error; err != nil {
		return err
	}
	invalid := make([]int64, 0)
	for _, selection := range selections {
		model := modelByID[selection.UserModelProviderGroupModelID]
		parent := parentByID[model.UserModelProviderID]
		if !RuntimeSupportsModelType(RuntimeProviderName(parent), model.ModelType) || !modelSupportsSelection(model, selection.ModelKey, parent.BaseURL) {
			invalid = append(invalid, selection.ID)
		}
	}
	if len(invalid) > 0 {
		return tx.Where("id IN ?", invalid).Delete(&orm.UserSelectedModel{}).Error
	}
	return nil
}
