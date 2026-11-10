package modelprovider

import (
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// guardEmbeddingRuntimeMutation protects the deployment-wide text index from
// edits to the runtime identity of its active model. Other embedding candidates
// remain editable. Core's dataset count alone cannot prove that Milvus has no
// orphaned vectors, so a recorded active model is protected even when that
// count is zero.
func guardEmbeddingRuntimeMutation(tx *gorm.DB, userID, providerID, groupID, modelID string) error {
	if !tx.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
		return nil
	}
	query := tx.Model(&orm.UserModelProviderGroupModel{}).
		Where("model_type IN ? AND deleted_at IS NULL", []string{"embed", "embedding", "embed_main"})
	if providerID != "" {
		query = query.Where("user_model_provider_id = ?", providerID)
	}
	if groupID != "" {
		query = query.Where("user_model_provider_group_id = ?", groupID)
	}
	if modelID != "" {
		query = query.Where("id = ?", modelID)
	}
	var ids []string
	if err := query.Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if tx.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		var state orm.EmbeddingIndexState
		err := tx.Where("scope = ?", globalEmbeddingScope).Take(&state).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			for _, id := range ids {
				if id == state.ActiveModelID {
					return errEmbeddingIdentityInUse
				}
			}
			// A recorded global choice supersedes old personal selections.
			return nil
		}
	}
	if tx.Migrator().HasTable(&orm.UserSelectedModel{}) {
		var selected int64
		if err := tx.Model(&orm.UserSelectedModel{}).
			Where("model_type = ? AND user_model_provider_group_model_id IN ?", "embed_main", ids).
			Count(&selected).Error; err != nil {
			return err
		}
		if selected > 0 {
			return errEmbeddingIdentityInUse
		}
	}
	if err := guardEmbeddingRemoval(tx, userID, ids...); err != nil {
		if errors.Is(err, errEmbeddingInUse) {
			return errEmbeddingIdentityInUse
		}
		return err
	}
	return nil
}
