package modelprovider

import (
	"errors"
	"net/http"

	"gorm.io/gorm"
	"lazymind/core/common"
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
	_ = userID // Kept for call-site compatibility; vector ownership is global.
	return guardEmbeddingDestructiveOperation(tx, errEmbeddingIdentityInUse, ids...)
}

func replyEmbeddingMutationConflict(w http.ResponseWriter, err error) bool {
	var conflict *embeddingDestructiveOperationError
	if errors.As(err, &conflict) {
		common.ReplyErrWithData(w, conflict.Error(), map[string]string{
			"reason": conflict.Reason, "recovery": "offline_vector_cleanup",
		}, http.StatusConflict)
		return true
	}
	// Preserve structured recovery for legacy direct sentinel paths while they
	// are phased into the evidence classifier. Never infer an active owner.
	if errors.Is(err, errEmbeddingInUse) || errors.Is(err, errEmbeddingIdentityInUse) {
		common.ReplyErrWithData(w, err.Error(), map[string]string{
			"reason": embeddingConflictIndexIdentityUnverified, "recovery": "offline_vector_cleanup",
		}, http.StatusConflict)
		return true
	}
	return false
}
