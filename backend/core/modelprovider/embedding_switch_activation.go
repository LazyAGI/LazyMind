package modelprovider

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// Only runtime identity is persisted in EmbeddingIndexState. Credentials stay in
// this request-local snapshot, and are revalidated under the provider lock.
type embeddingSwitchActivation struct {
	UserID, UserName, ModelID string
	Snapshot                  modelProbeSnapshot
}

func (target embeddingSwitchActivation) validate(ctx context.Context, tx *gorm.DB) (orm.UserModelProviderGroupModel, orm.UserModelProvider, error) {
	var model orm.UserModelProviderGroupModel
	if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", target.ModelID, target.UserID).Take(&model).Error; err != nil {
		return model, orm.UserModelProvider{}, errConnectionChanged
	}
	provider := orm.UserModelProvider{ID: model.UserModelProviderID, BaseModel: orm.BaseModel{CreateUserID: target.UserID}}
	if err := lockProvider(tx, &provider); err != nil {
		return model, provider, errConnectionChanged
	}
	if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", target.ModelID, target.UserID).Take(&model).Error; err != nil || model.UserModelProviderID != provider.ID {
		return model, provider, errConnectionChanged
	}
	current, unavailable, err := loadProbeSnapshot(ctx, tx, target.UserID, provider.ID, model.UserModelProviderGroupID, model.ID)
	if err != nil || unavailable != nil || current != target.Snapshot || !model.Enabled || !isTextEmbeddingModelType(model.ModelType) {
		return model, provider, errConnectionChanged
	}
	baseURL, _, err := ResolveProviderRuntimeCredential(ctx, tx, model.UserModelProviderGroupID)
	if err != nil || !modelSupportsSelectionForProvider(ctx, tx, provider, model, "embed_main", baseURL) {
		return model, provider, errConnectionChanged
	}
	return model, provider, nil
}

func (target embeddingSwitchActivation) run(ctx context.Context, db *gorm.DB, req EmbeddingClearRequest, signature string) error {
	// Browser cancellation must not strand the switch between purge and save.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	state, err := loadGlobalEmbeddingState(ctx, db)
	if err != nil {
		return errEmbeddingCleanupFailed
	}
	matchesTarget := state.TargetModelID == req.ModelID && state.TargetSource == req.Source && state.TargetSignature == signature
	if state.Phase == "ready" && state.Generation == req.Generation+1 && state.ActiveModelID == req.ModelID && state.ActiveSource == req.Source && state.Signature == signature {
		// The previous response may have been lost. Never delete the new index.
		return nil
	}
	resuming := matchesTarget && (state.Phase == "clearing" || state.Phase == "cleanup_failed" || state.Phase == "cleared")
	if state.Generation != req.Generation && (!resuming || state.Generation != req.Generation+1) {
		return errEmbeddingIndexBusy
	}
	if (state.Phase == "clearing" || state.TargetModelID != "") && !resuming {
		return errEmbeddingIndexBusy
	}
	if !resuming || state.Phase != "cleared" {
		if err := resetEmbeddingChunks(ctx, db, state, req.ModelID, req.Source, signature, false, &target); err != nil {
			return err
		}
		if !resuming {
			state.Generation++
		}
	}
	if err := target.activate(ctx, db, state.Generation, signature); err != nil {
		// The cleared phase and target survive a failed save for a safe retry.
		if errors.Is(err, errConnectionChanged) || errors.Is(err, errEmbeddingIndexBusy) {
			return err
		}
		return errEmbeddingCleanupFailed
	}
	return nil
}

func (target embeddingSwitchActivation) activate(ctx context.Context, db *gorm.DB, generation int64, signature string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Provider first, global state second: the same order as selection/edit.
		model, provider, err := target.validate(ctx, tx)
		if err != nil {
			return err
		}
		var state orm.EmbeddingIndexState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil {
			return err
		}
		if state.Generation != generation {
			return errEmbeddingIndexBusy
		}
		if state.Phase == "ready" && state.ActiveModelID == target.ModelID && state.ActiveSource == "own" && state.Signature == signature {
			return nil
		}
		if state.Phase != "cleared" || state.TargetModelID != target.ModelID || state.TargetSource != "own" || state.TargetSignature != signature {
			return errEmbeddingIndexBusy
		}
		now := time.Now().UTC()
		// Keep the personal selection and global runtime in one transaction.
		if err := tx.Model(&orm.UserSelectedModel{}).Where("model_type = ? AND share = ?", "embed_main", true).Updates(map[string]any{"share": false, "updated_at": now}).Error; err != nil {
			return err
		}
		var row orm.UserSelectedModel
		err = tx.Take(&row, "user_id = ? AND model_type = ?", target.UserID, "embed_main").Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row.UserID, row.UserName, row.ModelKey = target.UserID, target.UserName, "embed_main"
		row.UserModelProviderGroupModelID, row.Share, row.UpdatedAt = model.ID, true, now
		if row.CreatedAt.IsZero() {
			row.CreatedAt = now
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND model_type = ?", target.UserID, "embed_main").Delete(&orm.UserSelectedCloudModel{}).Error; err != nil {
			return err
		}
		state.ActiveModelID, state.ActiveSource, state.Signature = model.ID, "own", signature
		state.ActiveModelName, state.ActiveProviderName = model.Name, provider.Name
		state.TargetModelID, state.TargetSource, state.TargetSignature = "", "", ""
		state.Phase, state.UpdatedAt = "ready", now
		return tx.Save(&state).Error
	})
}
