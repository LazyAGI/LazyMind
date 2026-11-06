package modelprovider

import (
	"errors"
	"strings"
	"time"

	"lazymind/core/common/orm"

	"gorm.io/gorm"
)

func isTextEmbeddingModelType(modelType string) bool {
	switch strings.ToLower(strings.TrimSpace(modelType)) {
	case "embed", "embedding", "embed_main":
		return true
	default:
		return false
	}
}

var errEmbeddingInUse = errors.New("embedding model is in use; its knowledge-base vectors must be cleared before removal")

// guardEmbeddingRemoval protects the model that owns the deployment-wide text
// index. A different, unused embedding candidate can be removed freely.
// When upgrading a legacy installation with no recorded active model, a
// selected model or the sole configured text model is treated as in use.
func guardEmbeddingRemoval(tx *gorm.DB, userID string, modelIDs ...string) error {
	if len(modelIDs) == 0 {
		return nil
	}
	if tx.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		var state orm.EmbeddingIndexState
		err := tx.Where("scope = ?", globalEmbeddingScope).Take(&state).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && state.Phase == "cleared" && state.ActiveModelID == "" && state.TargetModelID == "" {
			// The offline maintenance handoff has already removed the shared
			// text vectors. Indexed dataset rows now describe pending reindex
			// work, so they must not keep the old model pinned.
			return nil
		}
		if err == nil && state.ActiveModelID != "" {
			for _, modelID := range modelIDs {
				if modelID == state.ActiveModelID {
					// Core rows cannot prove that Milvus contains no old text
					// collections, even after the last KB was deleted.
					return errEmbeddingInUse
				}
			}
			return nil
		}
		if errors.Is(err, gorm.ErrRecordNotFound) && tx.Migrator().HasTable(&orm.UserSelectedModel{}) {
			choice, present, choiceErr := ResolveGlobalEmbeddingChoice(tx.Statement.Context, tx)
			if choiceErr != nil {
				return choiceErr
			}
			if present && choice.Source == "own" {
				for _, modelID := range modelIDs {
					if modelID == choice.ModelID {
						return errEmbeddingInUse
					}
				}
			}
		}
	}
	if !tx.Migrator().HasTable(&orm.Dataset{}) {
		return nil
	}
	var count int64
	if err := tx.Model(&orm.Dataset{}).Where("processing_level = ? AND deleted_at IS NULL", "indexed").Count(&count).Error; err != nil {
		return err
	}
	if count == 0 && tx.Migrator().HasTable(&orm.DocumentProcessingState{}) {
		if err := tx.Table("document_processing_states s").
			Joins("JOIN datasets d ON d.id = s.dataset_id AND d.deleted_at IS NULL").
			Where("s.index_status = ?", "succeeded").Count(&count).Error; err != nil {
			return err
		}
	}
	if count == 0 {
		return nil
	}
	var anySelected int64
	if tx.Migrator().HasTable(&orm.UserSelectedModel{}) {
		var selected int64
		if err := tx.Model(&orm.UserSelectedModel{}).
			Where("model_type = ? AND user_model_provider_group_model_id IN ?", "embed_main", modelIDs).
			Count(&selected).Error; err != nil {
			return err
		}
		if selected > 0 {
			return errEmbeddingInUse
		}
		if err := tx.Model(&orm.UserSelectedModel{}).
			Where("model_type = ?", "embed_main").Count(&anySelected).Error; err != nil {
			return err
		}
	}
	var fallback int64
	if err := tx.Model(&orm.UserModelProviderGroupModel{}).
		Where("id IN ? AND is_default = ? AND deleted_at IS NULL", modelIDs, true).
		Count(&fallback).Error; err != nil {
		return err
	}
	if fallback > 0 {
		return errEmbeddingInUse
	}
	var defaultCandidates int64
	if err := tx.Model(&orm.UserModelProviderGroupModel{}).
		Where("model_type IN ? AND is_default = ? AND deleted_at IS NULL", []string{"embed", "embedding", "embed_main"}, true).
		Count(&defaultCandidates).Error; err != nil {
		return err
	}
	if anySelected > 0 || defaultCandidates > 0 {
		return nil
	}
	// With no state or selection there is no evidence that any candidate is
	// unused. Do not guess from the number of configured models.
	return errEmbeddingInUse
}

func clearEmbeddingStateAfterRemoval(tx *gorm.DB, modelIDs ...string) error {
	if len(modelIDs) == 0 || !tx.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		return nil
	}
	return tx.Model(&orm.EmbeddingIndexState{}).
		Where("scope = ? AND active_model_id IN ?", globalEmbeddingScope, modelIDs).
		Updates(map[string]any{
			"active_model_id": "", "active_source": "", "active_model_name": "", "active_provider_name": "",
			"signature": "", "phase": "ready", "generation": gorm.Expr("generation + 1"), "updated_at": time.Now().UTC(),
		}).Error
}
