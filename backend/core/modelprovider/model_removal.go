package modelprovider

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// Detach configuration immediately, without contacting the index service or
// acquiring its long-running job lock. Retained vectors are quarantined until
// an explicit cleanup succeeds. Provider -> state lock order matches selection.
func detachEmbeddingForModelRemoval(tx *gorm.DB, modelID string) error {
	if !tx.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		return nil
	}
	var state orm.EmbeddingIndexState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&state, "scope = ?", globalEmbeddingScope).Error
	missing := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !missing {
		return err
	}
	if missing {
		var selected int64
		if err := tx.Model(&orm.UserSelectedModel{}).Where("model_type = ? AND user_model_provider_group_model_id = ?", "embed_main", modelID).Count(&selected).Error; err != nil {
			return err
		}
		if selected == 0 {
			return nil
		}
		state.Scope = globalEmbeddingScope
	} else if !(state.ActiveSource == "own" && state.ActiveModelID == modelID) && !(state.TargetSource == "own" && state.TargetModelID == modelID) {
		return nil
	}
	state.ActiveModelID, state.ActiveSource, state.ActiveModelName, state.ActiveProviderName = "", "", "", ""
	state.TargetModelID, state.TargetSource, state.TargetSignature = "", "", ""
	state.Phase = "detached"
	state.Generation++
	state.UpdatedAt = time.Now().UTC()
	// Preserve the old signature as evidence; detached also protects unsigned
	// legacy/orphaned vectors when the local document inventory happens to be empty.
	if missing {
		return tx.Create(&state).Error
	}
	return tx.Save(&state).Error
}

// The caller holds the supplier lock and owns the credential transaction.
// All connections share the supplier's key selection and model inventory.
func removeBuiltinModelsAfterKeyDeletion(tx *gorm.DB, parent orm.UserModelProvider, now time.Time) (bool, error) {
	if parent.IsCustom || !hasSharedProviderEndpoint(parent) || !parent.HasCapability("has_models") || isLocalOpenAISupplier(tx.Statement.Context, tx, parent) {
		return false, nil
	}
	if !tx.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
		return false, nil
	}
	var models []orm.UserModelProviderGroupModel
	if err := tx.Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, parent.CreateUserID).Find(&models).Error; err != nil {
		return false, err
	}
	ids := make([]string, 0, len(models))
	hasMultimodal := false
	for _, model := range models {
		if err := detachEmbeddingForModelRemoval(tx, model.ID); err != nil {
			return false, err
		}
		ids = append(ids, model.ID)
		hasMultimodal = hasMultimodal || isMultimodalEmbeddingModelType(model.ModelType)
	}
	if len(ids) == 0 {
		return false, nil
	}
	if err := tx.Where("user_model_provider_group_model_id IN ?", ids).Delete(&orm.UserSelectedModel{}).Error; err != nil {
		return false, err
	}
	if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("id IN ?", ids).Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
		return false, err
	}
	return hasMultimodal, nil
}
