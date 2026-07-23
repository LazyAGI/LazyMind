package source

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

func (r *SQLRepository) GetSyncCheckpoint(ctx context.Context, bindingID string) (SyncCheckpoint, error) {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return SyncCheckpoint{}, err
	}
	var model ormSyncCheckpoint
	if err := db.Where("binding_id = ?", bindingID).First(&model).Error; err != nil {
		return SyncCheckpoint{}, mapGORMError(err, ErrCodeNotFound, "sync checkpoint not found")
	}
	return syncCheckpointFromORM(model), nil
}

func (r *SQLRepository) ListDueSyncCheckpoints(ctx context.Context, now time.Time, limit int) ([]SyncCheckpoint, error) {
	if limit <= 0 {
		limit = 50
	}
	db, err := r.sourceORM(ctx)
	if err != nil {
		return nil, err
	}
	var models []ormSyncCheckpoint
	err = db.Model(&ormSyncCheckpoint{}).
		Joins("JOIN source_bindings b ON b.binding_id = source_sync_checkpoints.binding_id").
		Where("source_sync_checkpoints.next_sync_at IS NOT NULL").
		Where("source_sync_checkpoints.next_sync_at <= ?", now).
		Where(`source_sync_checkpoints.lock_owner IS NULL OR source_sync_checkpoints.lock_owner = ''
				OR source_sync_checkpoints.lock_until IS NULL OR source_sync_checkpoints.lock_until <= ?`, now).
		Where("b.status IN ?", []string{"ACTIVE", "DELETING"}).
		Where("b.sync_mode IN ?", []string{"scheduled", "watch"}).
		Where("b.binding_generation = source_sync_checkpoints.binding_generation").
		Order("source_sync_checkpoints.next_sync_at, source_sync_checkpoints.binding_id").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, mapSQLConstraint(err)
	}
	checkpoints := make([]SyncCheckpoint, 0, len(models))
	for _, model := range models {
		checkpoints = append(checkpoints, syncCheckpointFromORM(model))
	}
	return checkpoints, nil
}

func (r *SQLRepository) SaveSyncCheckpoint(ctx context.Context, checkpoint SyncCheckpoint) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		if err := ormUpsertCheckpoint(tx, checkpoint); err != nil {
			return err
		}
		return updateBindingNextSyncAtORM(tx, checkpoint.BindingID, checkpoint.BindingGeneration, checkpoint.NextSyncAt, checkpoint.UpdatedAt)
	})
}

func (r *SQLRepository) RecordSyncJobError(ctx context.Context, sourceID, bindingID string, generation int64, lastError JSON, now time.Time) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		if err := recordBindingLastErrorORM(tx, sourceID, bindingID, generation, lastError, now); err != nil {
			return err
		}
		return recordCheckpointLastErrorORM(tx, bindingID, generation, lastError, now)
	})
}

func (r *SQLRepository) MarkBindingError(ctx context.Context, sourceID, bindingID string, generation int64, lastError JSON, now time.Time) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		if err := markBindingErrorORM(tx, sourceID, bindingID, generation, lastError, now); err != nil {
			return err
		}
		return recordCheckpointLastErrorORM(tx, bindingID, generation, lastError, now)
	})
}

func (r *SQLRepository) sourceORM(ctx context.Context) (*gorm.DB, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return nil, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	return db, nil
}

func mapGORMError(err error, code ErrorCode, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NewStoreError(code, message)
	}
	return mapSQLConstraint(err)
}

func recordCheckpointLastErrorORM(db *gorm.DB, bindingID string, generation int64, lastError JSON, now time.Time) error {
	result := db.Model(&ormSyncCheckpoint{}).
		Where("binding_id = ? AND binding_generation = ?", bindingID, generation).
		Updates(map[string]any{"last_error": lastError, "updated_at": now})
	if result.Error != nil {
		return mapSQLConstraint(result.Error)
	}
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeGenerationConflict, "sync checkpoint generation is stale")
	}
	return nil
}

func recordBindingLastErrorORM(db *gorm.DB, sourceID, bindingID string, generation int64, lastError JSON, now time.Time) error {
	result := db.Model(&ormBinding{}).
		Where("source_id = ? AND binding_id = ? AND binding_generation = ?", sourceID, bindingID, generation).
		Updates(map[string]any{"last_error": lastError, "updated_at": now})
	if result.Error != nil {
		return mapSQLConstraint(result.Error)
	}
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeGenerationConflict, "binding generation is stale")
	}
	return nil
}

func markBindingErrorORM(db *gorm.DB, sourceID, bindingID string, generation int64, lastError JSON, now time.Time) error {
	result := db.Model(&ormBinding{}).
		Where("source_id = ? AND binding_id = ? AND binding_generation = ?", sourceID, bindingID, generation).
		Updates(map[string]any{"status": "ERROR", "last_error": lastError, "updated_at": now})
	if result.Error != nil {
		return mapSQLConstraint(result.Error)
	}
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeGenerationConflict, "binding generation is stale")
	}
	return nil
}

func syncCheckpointORMValues(checkpoint SyncCheckpoint) map[string]any {
	return map[string]any{
		"source_id":          checkpoint.SourceID,
		"binding_id":         checkpoint.BindingID,
		"binding_generation": checkpoint.BindingGeneration,
		"cursor":             nullString(checkpoint.Cursor),
		"next_sync_at":       checkpoint.NextSyncAt,
		"last_sync_at":       checkpoint.LastSyncAt,
		"last_success_at":    checkpoint.LastSuccessAt,
		"lock_owner":         nullString(checkpoint.LockOwner),
		"lock_until":         checkpoint.LockUntil,
		"retry_count":        checkpoint.RetryCount,
		"last_error":         checkpoint.LastError,
		"created_at":         checkpoint.CreatedAt,
		"updated_at":         checkpoint.UpdatedAt,
	}
}

func syncCheckpointFromORM(model ormSyncCheckpoint) SyncCheckpoint {
	return SyncCheckpoint{
		SourceID:          model.SourceID,
		BindingID:         model.BindingID,
		BindingGeneration: model.BindingGeneration,
		Cursor:            model.Cursor,
		NextSyncAt:        model.NextSyncAt,
		LastSyncAt:        model.LastSyncAt,
		LastSuccessAt:     model.LastSuccessAt,
		LockOwner:         model.LockOwner,
		LockUntil:         model.LockUntil,
		RetryCount:        model.RetryCount,
		LastError:         model.LastError,
		CreatedAt:         model.CreatedAt,
		UpdatedAt:         model.UpdatedAt,
	}
}

func updateBindingNextSyncAtORM(db *gorm.DB, bindingID string, generation int64, nextSyncAt *time.Time, now time.Time) error {
	query := db.Model(&ormBinding{}).Where("binding_id = ?", bindingID)
	if generation != 0 {
		query = query.Where("binding_generation = ?", generation)
	}
	result := query.Updates(map[string]any{
		"next_sync_at": nextSyncAt,
		"updated_at":   now,
	})
	if result.Error != nil {
		return mapSQLConstraint(result.Error)
	}
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeGenerationConflict, "binding generation is stale")
	}
	return nil
}
