package source

import (
	"context"
	"errors"
	"log"
	"strings"
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

func (r *SQLRepository) ListBindingsBySourceIDs(ctx context.Context, sourceIDs []string) ([]Binding, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return nil, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	ids := uniqueNonEmptyStoreStrings(sourceIDs)
	if len(ids) == 0 {
		return []Binding{}, nil
	}
	var rows []ormBinding
	if err := db.Where("source_id IN ? AND status <> ?", ids, "DELETING").Order("source_id, binding_id").Find(&rows).Error; err != nil {
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

func (r *SQLRepository) ListReadyBindingCleanups(ctx context.Context, limit int) ([]Binding, error) {
	if limit <= 0 {
		limit = 50
	}
	db := r.ormDB(ctx)
	if db == nil {
		return nil, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var rows []ormBinding
	err := db.Model(&ormBinding{}).
		Joins("JOIN sources s ON s.source_id = source_bindings.source_id AND s.status = ?", "ACTIVE").
		Where("source_bindings.status = ?", "DELETING").
		Where(`EXISTS (
			SELECT 1 FROM source_sync_runs sr
			WHERE sr.source_id = source_bindings.source_id
			  AND sr.binding_id = source_bindings.binding_id
			  AND sr.binding_generation = source_bindings.binding_generation
			  AND sr.scope_type = ?
			  AND sr.status = ?
		)`, "cleanup", SyncRunStatusSucceeded).
		Where(`NOT EXISTS (
			SELECT 1 FROM source_document_states ds
			WHERE ds.source_id = source_bindings.source_id
			  AND ds.binding_id = source_bindings.binding_id
			  AND ds.pending_action = ?
		)`, "DELETE").
		Where(`NOT EXISTS (
			SELECT 1 FROM parse_tasks pt
			WHERE pt.source_id = source_bindings.source_id
			  AND pt.binding_id = source_bindings.binding_id
			  AND pt.task_action = ?
			  AND pt.status IN ?
		)`, "DELETE", []string{"PENDING", "RUNNING", "SUBMITTED"}).
		Order("source_bindings.deleted_at, source_bindings.binding_id").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, mapSQLConstraint(err)
	}
	bindings := make([]Binding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, bindingFromORM(row))
	}
	return bindings, nil
}

func (r *SQLRepository) FinalizeBindingCleanup(ctx context.Context, sourceID, bindingID string, now time.Time) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		var row ormBinding
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND binding_id = ?", sourceID, bindingID).First(&row).Error; err != nil {
			return mapORMNotFound(err, ErrCodeBindingNotFound, "binding not found")
		}
		binding := bindingFromORM(row)
		if binding.Status != "DELETING" {
			return NewStoreError(ErrCodeGenerationConflict, "binding is not deleting")
		}
		var pending int64
		if err := tx.Model(&ormDocumentState{}).Where("source_id = ? AND binding_id = ? AND pending_action = ?", sourceID, bindingID, "DELETE").Count(&pending).Error; err != nil {
			return mapSQLConstraint(err)
		}
		if pending > 0 {
			return NewStoreError(ErrCodeGenerationConflict, "binding cleanup is still pending")
		}
		if _, err := cleanupBindingGenerationORMTx(tx, sourceID, bindingID, 0, "binding cleanup finalized", now); err != nil {
			return err
		}
		if err := tx.Where("binding_id = ?", bindingID).Delete(&ormSyncCheckpoint{}).Error; err != nil {
			return mapSQLConstraint(err)
		}
		return mapSQLConstraint(tx.Where("source_id = ? AND binding_id = ?", sourceID, bindingID).Delete(&ormBinding{}).Error)
	})
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
	if err := releaseCheckpointLockORMTx(tx, sourceID, bindingID, deletedAt); err != nil {
		return Binding{}, CleanupResult{}, err
	}
	cleanup, err := prepareBindingCleanupORMTx(tx, sourceID, bindingID, binding.BindingGeneration, "binding delete cleanup", deletedAt)
	if err != nil {
		return Binding{}, CleanupResult{}, err
	}
	return binding, cleanup, nil
}

func (r *SQLRepository) deleteBindingImmediatelyTx(ctx context.Context, tx *gorm.DB, sourceID, bindingID string, deletedAt time.Time) (Binding, CleanupResult, error) {
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

func releaseCheckpointLockORMTx(tx *gorm.DB, sourceID, bindingID string, now time.Time) error {
	err := tx.Model(&ormSyncCheckpoint{}).
		Where("binding_id = ?", bindingID).
		Updates(map[string]any{
			"source_id":  sourceID,
			"lock_owner": nil,
			"lock_until": nil,
			"updated_at": now,
		}).Error
	return mapSQLConstraint(err)
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

func prepareBindingCleanupORMTx(tx *gorm.DB, sourceID, bindingID string, generation int64, reason string, now time.Time) (CleanupResult, error) {
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

	var rows []ormDocumentState
	if err := tx.Where("source_id = ? AND binding_id = ?", sourceID, bindingID).Find(&rows).Error; err != nil {
		return CleanupResult{}, mapSQLConstraint(err)
	}
	var documents []ormDocument
	if err := tx.Where("source_id = ? AND binding_id = ?", sourceID, bindingID).Find(&documents).Error; err != nil {
		return CleanupResult{}, mapSQLConstraint(err)
	}
	documentsByObject := make(map[string]ormDocument, len(documents))
	for _, document := range documents {
		documentsByObject[document.ObjectKey] = document
	}
	for _, row := range rows {
		state := documentStateFromORM(row)
		document := documentsByObject[state.ObjectKey]
		synced := strings.TrimSpace(state.BaselineVersion) != "" || strings.TrimSpace(document.CoreDocumentID) != ""
		state.BindingGeneration = generation
		state.ParseQueueState = "NONE"
		state.ActiveTaskID = ""
		state.LastError = JSON{}
		state.UpdatedAt = now
		if synced {
			if strings.TrimSpace(state.BaselineVersion) == "" {
				state.BaselineVersion = firstNonEmptyStoreString(document.CurrentVersionID, document.SourceVersion, state.SourceVersion)
			}
			state.SourceState = "OUT_OF_SCOPE"
			state.PendingAction = "DELETE"
			state.DocumentListVisible = true
			state.Selectable = true
		} else {
			state.SourceState = "UNCHANGED"
			state.PendingAction = ""
			state.DocumentListVisible = false
			state.Selectable = false
			state.DocumentID = ""
		}
		if err := saveDocumentStateORMTx(tx, state); err != nil {
			return CleanupResult{}, err
		}
	}
	result.CleanupIntents = append(result.CleanupIntents, CleanupIntent{Kind: "binding_cleanup_pending", Reason: reason, CreatedAt: now})
	return result, nil
}

func firstNonEmptyStoreString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
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

func uniqueNonEmptyStoreStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
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

func (r *SQLRepository) UpdateBindingChatEnabled(ctx context.Context, bindingID string, chatEnabled bool) error {
	log.Printf("[BINDING_CHAT_REPO] updating binding_id=%s chat_enabled=%v", bindingID, chatEnabled)
	result := r.orm.WithContext(ctx).
		Model(&ormBinding{}).
		Where("binding_id = ?", bindingID).
		Updates(map[string]any{"chat_enabled": chatEnabled})
	if result.Error != nil {
		log.Printf("[BINDING_CHAT_REPO] update error: %v", result.Error)
		return result.Error
	}
	log.Printf("[BINDING_CHAT_REPO] rows_affected=%d", result.RowsAffected)
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeBindingNotFound, "binding not found")
	}
	return nil
}
