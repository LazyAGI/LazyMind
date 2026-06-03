package source

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *SQLRepository) CreateSyncRun(ctx context.Context, run SyncRun) error {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return err
	}
	return insertSyncRunORM(db, run)
}

func (r *SQLRepository) EnqueueSyncRun(ctx context.Context, run SyncRun) (SyncRun, bool, error) {
	var out SyncRun
	var created bool
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var checkpointModel ormSyncCheckpoint
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("binding_id = ?", run.BindingID).First(&checkpointModel).Error; err != nil {
			return mapGORMError(err, ErrCodeNotFound, "sync checkpoint not found")
		}
		checkpoint := syncCheckpointFromORM(checkpointModel)
		if checkpoint.BindingGeneration != run.BindingGeneration {
			return NewStoreError(ErrCodeGenerationConflict, "sync run generation is stale")
		}
		var existingModel ormSyncRun
		if err := tx.Where("run_id = ?", run.RunID).First(&existingModel).Error; err == nil {
			out = syncRunFromORM(existingModel)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		existing, ok, err := queryActiveSyncRunORM(tx, run.BindingID, run.BindingGeneration)
		if err != nil || ok {
			out = existing
			return err
		}
		if run.Status == "" {
			run.Status = SyncRunStatusPending
		}
		if err := insertSyncRunORM(tx, run); err != nil {
			return err
		}
		out = run
		created = true
		return nil
	})
	return out, created, err
}

func (r *SQLRepository) GetSyncRun(ctx context.Context, runID string) (SyncRun, error) {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return SyncRun{}, err
	}
	var model ormSyncRun
	if err := db.Where("run_id = ?", runID).First(&model).Error; err != nil {
		return SyncRun{}, mapGORMError(err, ErrCodeNotFound, "sync run not found")
	}
	return syncRunFromORM(model), nil
}

func (r *SQLRepository) ClaimDueSyncRun(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (SyncRun, bool, error) {
	var run SyncRun
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var taskModel ormSyncRun
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND started_at <= ?", SyncRunStatusPending, now).
			Where(`EXISTS (
				SELECT 1
				FROM source_sync_checkpoints c
				JOIN source_bindings b ON b.binding_id = c.binding_id
				WHERE c.binding_id = source_sync_runs.binding_id
				  AND c.binding_generation = source_sync_runs.binding_generation
				  AND b.status = ?
				  AND (c.lock_owner IS NULL OR c.lock_owner = '' OR c.lock_until IS NULL OR c.lock_until <= ?)
			)`, "ACTIVE", now).
			Order("started_at, run_id").
			Limit(1).
			First(&taskModel).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		task := syncRunFromORM(taskModel)
		var checkpointModel ormSyncCheckpoint
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("binding_id = ?", task.BindingID).First(&checkpointModel).Error; err != nil {
			return mapGORMError(err, ErrCodeNotFound, "sync checkpoint not found")
		}
		checkpoint := syncCheckpointFromORM(checkpointModel)
		if checkpoint.BindingGeneration != task.BindingGeneration || !syncCheckpointLockAvailable(checkpoint, now) {
			return nil
		}
		task.Status = SyncRunStatusRunning
		task.StartedAt = now
		if err := updateSyncRunORM(tx, task); err != nil {
			return err
		}
		checkpoint.LockOwner = workerID
		checkpoint.LockUntil = timePtr(now.Add(ttl))
		checkpoint.LastSyncAt = timePtr(now)
		checkpoint.UpdatedAt = now
		if err := ormUpsertCheckpoint(tx, checkpoint); err != nil {
			return err
		}
		run = task
		return nil
	})
	if err != nil {
		return SyncRun{}, false, err
	}
	return run, run.RunID != "", nil
}

func (r *SQLRepository) FinishSyncRun(ctx context.Context, runID, workerID string, finish SyncRunFinish) (SyncRun, bool, error) {
	var run SyncRun
	var finished bool
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var currentModel ormSyncRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ?", runID).First(&currentModel).Error; err != nil {
			return mapGORMError(err, ErrCodeNotFound, "sync run not found")
		}
		current := syncRunFromORM(currentModel)
		var checkpointModel ormSyncCheckpoint
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("binding_id = ?", current.BindingID).First(&checkpointModel).Error; err != nil {
			return mapGORMError(err, ErrCodeNotFound, "sync checkpoint not found")
		}
		checkpoint := syncCheckpointFromORM(checkpointModel)
		run = current
		if current.Status != SyncRunStatusRunning || checkpoint.LockOwner != workerID {
			return nil
		}
		applySyncRunFinish(&current, finish)
		applyCheckpointFinish(&checkpoint, finish)
		if err := updateSyncRunORM(tx, current); err != nil {
			return err
		}
		if err := ormUpsertCheckpoint(tx, checkpoint); err != nil {
			return err
		}
		if err := updateBindingNextSyncAtORM(tx, current.BindingID, current.BindingGeneration, checkpoint.NextSyncAt, finish.FinishedAt); err != nil {
			return err
		}
		run = current
		finished = true
		return nil
	})
	return run, finished, err
}

func syncRunORMValues(run SyncRun) map[string]any {
	return map[string]any{
		"run_id":             run.RunID,
		"source_id":          run.SourceID,
		"binding_id":         run.BindingID,
		"binding_generation": run.BindingGeneration,
		"trigger_type":       run.TriggerType,
		"scheduled_fire_at":  run.ScheduledFireAt,
		"scope_type":         run.ScopeType,
		"scope_ref_json":     run.ScopeRef,
		"coverage_json":      run.Coverage,
		"status":             run.Status,
		"seen_count":         run.SeenCount,
		"new_count":          run.NewCount,
		"modified_count":     run.ModifiedCount,
		"deleted_count":      run.DeletedCount,
		"unchanged_count":    run.UnchangedCount,
		"error_code":         nullString(run.ErrorCode),
		"error_message":      nullString(run.ErrorMessage),
		"started_at":         run.StartedAt,
		"finished_at":        run.FinishedAt,
	}
}

func syncRunFromORM(model ormSyncRun) SyncRun {
	return SyncRun{
		RunID:             model.RunID,
		SourceID:          model.SourceID,
		BindingID:         model.BindingID,
		BindingGeneration: model.BindingGeneration,
		TriggerType:       model.TriggerType,
		ScheduledFireAt:   model.ScheduledFireAt,
		ScopeType:         model.ScopeType,
		ScopeRef:          model.ScopeRef,
		Coverage:          model.Coverage,
		Status:            model.Status,
		SeenCount:         model.SeenCount,
		NewCount:          model.NewCount,
		ModifiedCount:     model.ModifiedCount,
		DeletedCount:      model.DeletedCount,
		UnchangedCount:    model.UnchangedCount,
		ErrorCode:         model.ErrorCode,
		ErrorMessage:      model.ErrorMessage,
		StartedAt:         model.StartedAt,
		FinishedAt:        model.FinishedAt,
	}
}

func insertSyncRunORM(db *gorm.DB, run SyncRun) error {
	return mapSQLConstraint(db.Model(&ormSyncRun{}).Create(syncRunORMValues(run)).Error)
}

func updateSyncRunORM(db *gorm.DB, run SyncRun) error {
	err := db.Model(&ormSyncRun{}).Where("run_id = ?", run.RunID).Updates(map[string]any{
		"status":          run.Status,
		"coverage_json":   run.Coverage,
		"seen_count":      run.SeenCount,
		"new_count":       run.NewCount,
		"modified_count":  run.ModifiedCount,
		"deleted_count":   run.DeletedCount,
		"unchanged_count": run.UnchangedCount,
		"error_code":      nullString(run.ErrorCode),
		"error_message":   nullString(run.ErrorMessage),
		"started_at":      run.StartedAt,
		"finished_at":     run.FinishedAt,
	}).Error
	return mapSQLConstraint(err)
}

func queryActiveSyncRunORM(db *gorm.DB, bindingID string, generation int64) (SyncRun, bool, error) {
	var model ormSyncRun
	err := db.Where("binding_id = ? AND binding_generation = ?", bindingID, generation).
		Where("status IN ?", []string{SyncRunStatusPending, SyncRunStatusRunning}).
		Order("started_at, run_id").
		Limit(1).
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SyncRun{}, false, nil
		}
		return SyncRun{}, false, mapSQLConstraint(err)
	}
	return syncRunFromORM(model), true, nil
}
