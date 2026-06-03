package source

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *SQLRepository) ListBindings(ctx context.Context, sourceID string) ([]Binding, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return nil, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var rows []ormBinding
	if err := db.Where("source_id = ?", sourceID).Order("binding_id").Find(&rows).Error; err != nil {
		return nil, mapSQLConstraint(err)
	}
	bindings := make([]Binding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, bindingFromORM(row))
	}
	return bindings, nil
}

func (r *SQLRepository) GetBinding(ctx context.Context, sourceID, bindingID string) (Binding, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return Binding{}, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var binding ormBinding
	if err := db.Where("source_id = ? AND binding_id = ?", sourceID, bindingID).First(&binding).Error; err != nil {
		return Binding{}, mapORMNotFound(err, ErrCodeBindingNotFound, "binding not found")
	}
	return bindingFromORM(binding), nil
}

func (r *SQLRepository) FindActiveBindingByTarget(ctx context.Context, sourceID, excludeBindingID, connectorType, targetType, targetFingerprint string) (Binding, bool, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return Binding{}, false, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var binding ormBinding
	err := db.Where(
		"source_id = ? AND binding_id <> ? AND connector_type = ? AND target_type = ? AND target_fingerprint = ? AND status <> ?",
		sourceID, excludeBindingID, connectorType, targetType, targetFingerprint, "DELETING",
	).First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Binding{}, false, nil
		}
		return Binding{}, false, mapSQLConstraint(err)
	}
	return bindingFromORM(binding), true, nil
}

func (r *SQLRepository) AddBinding(ctx context.Context, binding Binding, checkpoint SyncCheckpoint) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		if err := ormInsertBinding(tx, binding); err != nil {
			return err
		}
		return ormUpsertCheckpoint(tx, checkpoint)
	})
}

func (r *SQLRepository) UpdateBinding(ctx context.Context, binding Binding, checkpoint SyncCheckpoint, cleanup BindingUpdateCleanup) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		if err := ormUpdateBinding(tx, binding); err != nil {
			return err
		}
		if err := ormUpsertCheckpoint(tx, checkpoint); err != nil {
			return err
		}
		if cleanup.CancelPendingScheduled {
			if _, err := cancelPendingScheduledSyncRunsORMTx(tx, binding.SourceID, binding.BindingID, binding.BindingGeneration, cleanup.Reason, binding.UpdatedAt); err != nil {
				return err
			}
		}
		if cleanup.ClearIndexedState {
			if _, err := cleanupBindingGenerationORMTx(tx, binding.SourceID, binding.BindingID, cleanup.OldBindingGeneration, cleanup.Reason, binding.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SQLRepository) DeleteBinding(ctx context.Context, sourceID, bindingID string, deletedAt time.Time) (BindingDeleteResult, error) {
	var result BindingDeleteResult
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		binding, cleanup, err := r.softDeleteBindingTx(ctx, tx, sourceID, bindingID, deletedAt)
		if err != nil {
			return err
		}
		result = BindingDeleteResult{Binding: binding, Cleanup: cleanup}
		return nil
	})
	return result, err
}

func (r *SQLRepository) softDeleteBindingTx(ctx context.Context, tx *gorm.DB, sourceID, bindingID string, deletedAt time.Time) (Binding, CleanupResult, error) {
	var row ormBinding
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND binding_id = ?", sourceID, bindingID).First(&row).Error; err != nil {
		return Binding{}, CleanupResult{}, mapORMNotFound(err, ErrCodeBindingNotFound, "binding not found")
	}
	binding := bindingFromORM(row)
	binding.Status = "DELETING"
	binding.DeletedAt = &deletedAt
	binding.UpdatedAt = deletedAt
	if err := ormUpdateBinding(tx, binding); err != nil {
		return Binding{}, CleanupResult{}, err
	}
	if err := stopCheckpointORMTx(tx, sourceID, bindingID, deletedAt); err != nil {
		return Binding{}, CleanupResult{}, err
	}
	cleanup, err := cleanupBindingGenerationORMTx(tx, sourceID, bindingID, 0, "binding delete cleanup", deletedAt)
	if err != nil {
		return Binding{}, CleanupResult{}, err
	}
	return binding, cleanup, nil
}

func stopCheckpointORMTx(tx *gorm.DB, sourceID, bindingID string, now time.Time) error {
	err := tx.Model(&ormSyncCheckpoint{}).
		Where("binding_id = ?", bindingID).
		Updates(map[string]any{
			"source_id":    sourceID,
			"next_sync_at": nil,
			"lock_owner":   nil,
			"lock_until":   nil,
			"updated_at":   now,
		}).Error
	return mapSQLConstraint(err)
}

func cleanupBindingGenerationORMTx(tx *gorm.DB, sourceID, bindingID string, generation int64, reason string, now time.Time) (CleanupResult, error) {
	if reason == "" {
		reason = "binding cleanup"
	}
	var result CleanupResult
	count, err := cancelSyncRunsORMTx(tx, sourceID, bindingID, generation, reason, now)
	if err != nil {
		return CleanupResult{}, err
	}
	result.CancelledSyncRunCount = count
	count, err = cancelParseTasksORMTx(tx, sourceID, bindingID, generation, reason, now)
	if err != nil {
		return CleanupResult{}, err
	}
	result.CancelledParseTaskCount = count
	count, err = deleteObjectsByBindingORMTx(tx, sourceID, bindingID)
	if err != nil {
		return CleanupResult{}, err
	}
	result.ClearedObjectCount = count
	count, err = deleteStatesByBindingORMTx(tx, sourceID, bindingID)
	if err != nil {
		return CleanupResult{}, err
	}
	result.ClearedStateCount = count
	count, err = tombstoneDocumentsORMTx(tx, sourceID, bindingID, now)
	if err != nil {
		return CleanupResult{}, err
	}
	result.TombstonedDocumentCount = count
	result.CleanupIntents = append(result.CleanupIntents, CleanupIntent{Kind: "binding_cleanup", Reason: reason, CreatedAt: now})
	return result, nil
}

func cancelSyncRunsORMTx(tx *gorm.DB, sourceID, bindingID string, generation int64, reason string, now time.Time) (int64, error) {
	query := tx.Model(&ormSyncRun{}).
		Where("source_id = ? AND binding_id = ? AND status IN ?", sourceID, bindingID, []string{"PENDING", "RUNNING"})
	if generation != 0 {
		query = query.Where("binding_generation = ?", generation)
	}
	res := query.Updates(map[string]any{
		"status":        "CANCELED",
		"error_code":    "CANCELED",
		"error_message": reason,
		"finished_at":   now,
	})
	return res.RowsAffected, mapSQLConstraint(res.Error)
}

func cancelPendingScheduledSyncRunsORMTx(tx *gorm.DB, sourceID, bindingID string, generation int64, reason string, now time.Time) (int64, error) {
	if reason == "" {
		reason = "binding schedule changed"
	}
	query := tx.Model(&ormSyncRun{}).
		Where("source_id = ? AND binding_id = ? AND status = ? AND trigger_type = ?", sourceID, bindingID, SyncRunStatusPending, "scheduled")
	if generation != 0 {
		query = query.Where("binding_generation = ?", generation)
	}
	res := query.Updates(map[string]any{
		"status":        "CANCELED",
		"error_code":    "CANCELED",
		"error_message": reason,
		"finished_at":   now,
	})
	return res.RowsAffected, mapSQLConstraint(res.Error)
}

func cancelParseTasksORMTx(tx *gorm.DB, sourceID, bindingID string, generation int64, reason string, now time.Time) (int64, error) {
	query := tx.Model(&ormParseTask{}).
		Where("source_id = ? AND binding_id = ? AND status IN ?", sourceID, bindingID, []string{"PENDING", "RUNNING", "SUBMITTED"})
	if generation != 0 {
		query = query.Where("binding_generation = ?", generation)
	}
	res := query.Updates(map[string]any{
		"status":      "SUPERSEDED",
		"lease_owner": nil,
		"lease_until": nil,
		"last_error":  JSON{"reason": reason},
		"updated_at":  now,
	})
	return res.RowsAffected, mapSQLConstraint(res.Error)
}

func deleteObjectsByBindingORMTx(tx *gorm.DB, sourceID, bindingID string) (int64, error) {
	res := tx.Where("source_id = ? AND binding_id = ?", sourceID, bindingID).Delete(&ormSourceObject{})
	return res.RowsAffected, mapSQLConstraint(res.Error)
}

func deleteStatesByBindingORMTx(tx *gorm.DB, sourceID, bindingID string) (int64, error) {
	res := tx.Where("source_id = ? AND binding_id = ?", sourceID, bindingID).Delete(&ormDocumentState{})
	return res.RowsAffected, mapSQLConstraint(res.Error)
}

func tombstoneDocumentsORMTx(tx *gorm.DB, sourceID, bindingID string, now time.Time) (int64, error) {
	res := tx.Model(&ormDocument{}).
		Where("source_id = ? AND binding_id = ? AND parse_status <> ?", sourceID, bindingID, "SUPERSEDED").
		Updates(map[string]any{
			"parse_status": "SUPERSEDED",
			"updated_at":   now,
		})
	return res.RowsAffected, mapSQLConstraint(res.Error)
}

func ormInsertBinding(db *gorm.DB, binding Binding) error {
	err := db.Table("source_bindings").Create(bindingCreateValues(binding)).Error
	return mapSQLConstraint(err)
}

func ormUpdateBinding(db *gorm.DB, binding Binding) error {
	err := db.Model(&ormBinding{}).Where("binding_id = ?", binding.BindingID).Updates(bindingUpdateValues(binding)).Error
	return mapSQLConstraint(err)
}

func bindingCreateValues(binding Binding) map[string]any {
	values := bindingUpdateValues(binding)
	values["binding_id"] = binding.BindingID
	values["created_at"] = binding.CreatedAt
	return values
}

func bindingUpdateValues(binding Binding) map[string]any {
	return map[string]any{
		"source_id":                 binding.SourceID,
		"binding_type":              binding.BindingType,
		"connector_type":            binding.ConnectorType,
		"target_type":               binding.TargetType,
		"target_ref":                binding.TargetRef,
		"target_fingerprint":        binding.TargetFingerprint,
		"agent_id":                  nullString(binding.AgentID),
		"auth_connection_id":        nullString(binding.AuthConnectionID),
		"provider_options_json":     binding.ProviderOptions,
		"tree_key":                  binding.TreeKey,
		"binding_generation":        binding.BindingGeneration,
		"core_parent_document_id":   binding.CoreParentDocumentID,
		"core_parent_document_name": binding.CoreParentDocumentName,
		"sync_mode":                 binding.SyncMode,
		"schedule_policy_json":      binding.SchedulePolicy,
		"next_sync_at":              binding.NextSyncAt,
		"include_extensions_json":   binding.IncludeExtensions,
		"exclude_extensions_json":   binding.ExcludeExtensions,
		"status":                    binding.Status,
		"last_error":                binding.LastError,
		"deleted_at":                binding.DeletedAt,
		"updated_at":                binding.UpdatedAt,
	}
}

func ormUpsertCheckpoint(db *gorm.DB, checkpoint SyncCheckpoint) error {
	err := db.Table("source_sync_checkpoints").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "binding_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"source_id", "binding_generation", "cursor", "next_sync_at", "last_sync_at",
			"last_success_at", "lock_owner", "lock_until", "retry_count", "last_error", "updated_at",
		}),
	}).Create(map[string]any{
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
	}).Error
	return mapSQLConstraint(err)
}
