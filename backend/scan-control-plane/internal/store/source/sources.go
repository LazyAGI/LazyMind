package source

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *SQLRepository) CreateSourceWithBindings(ctx context.Context, record SourceCreateRecord) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		if err := ormInsertSource(tx, record.Source); err != nil {
			return err
		}
		for _, binding := range record.Bindings {
			if err := ormInsertBinding(tx, binding); err != nil {
				return err
			}
		}
		for _, checkpoint := range record.Checkpoints {
			if err := ormUpsertCheckpoint(tx, checkpoint); err != nil {
				return err
			}
		}
		return ormUpsertOperation(tx, record.Operation)
	})
}

func (r *SQLRepository) ListSources(ctx context.Context, req SourceListRequest) ([]SourceListRecord, int, error) {
	page, pageSize := normalizeSQLPage(req.Page, req.PageSize)
	offset := (page - 1) * pageSize
	db := r.ormDB(ctx)
	if db == nil {
		return nil, 0, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	query := applySourceListFilters(db.Table("sources AS s").Where("s.deleted_at IS NULL"), req)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, mapSQLConstraint(err)
	}

	var rows []sourceListORMRow
	err := applySourceListFilters(db.Table("sources AS s").Where("s.deleted_at IS NULL"), req).
		Select(sourceListSelectSQL()).
		Joins("LEFT JOIN source_bindings b ON b.source_id = s.source_id AND b.status <> ?", "DELETING").
		Group("s.source_id").
		Order("s.updated_at DESC, s.source_id").
		Limit(pageSize).
		Offset(offset).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	records := make([]SourceListRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, SourceListRecord{Source: sourceFromORM(row.source()), BindingCount: row.BindingCount})
	}
	return records, int(total), nil
}

type sourceListORMRow struct {
	SourceID          string     `gorm:"column:source_id"`
	TenantID          string     `gorm:"column:tenant_id"`
	CreatedBy         string     `gorm:"column:created_by"`
	Name              string     `gorm:"column:name"`
	DatasetID         string     `gorm:"column:dataset_id"`
	Status            string     `gorm:"column:status"`
	SourceOptions     JSON       `gorm:"column:source_options_json;type:jsonb"`
	IncludeExtensions JSON       `gorm:"column:include_extensions_json;type:jsonb"`
	ExcludeExtensions JSON       `gorm:"column:exclude_extensions_json;type:jsonb"`
	ConfigVersion     int64      `gorm:"column:config_version"`
	DeletedAt         *time.Time `gorm:"column:deleted_at"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
	BindingCount      int        `gorm:"column:binding_count"`
}

func (row sourceListORMRow) source() ormSource {
	return ormSource{
		SourceID:          row.SourceID,
		TenantID:          row.TenantID,
		CreatedBy:         row.CreatedBy,
		Name:              row.Name,
		DatasetID:         row.DatasetID,
		Status:            row.Status,
		SourceOptions:     row.SourceOptions,
		IncludeExtensions: row.IncludeExtensions,
		ExcludeExtensions: row.ExcludeExtensions,
		ConfigVersion:     row.ConfigVersion,
		DeletedAt:         row.DeletedAt,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

func sourceListSelectSQL() string {
	return strings.Join([]string{
		"s.source_id AS source_id",
		"s.tenant_id AS tenant_id",
		"s.created_by AS created_by",
		"s.name AS name",
		"s.dataset_id AS dataset_id",
		"s.status AS status",
		"s.source_options_json AS source_options_json",
		"s.include_extensions_json AS include_extensions_json",
		"s.exclude_extensions_json AS exclude_extensions_json",
		"s.config_version AS config_version",
		"s.deleted_at AS deleted_at",
		"s.created_at AS created_at",
		"s.updated_at AS updated_at",
		"COUNT(b.binding_id) AS binding_count",
	}, ", ")
}

func (r *SQLRepository) GetSource(ctx context.Context, sourceID string) (Source, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return Source{}, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var source ormSource
	if err := db.Where("source_id = ?", sourceID).First(&source).Error; err != nil {
		return Source{}, mapORMNotFound(err, ErrCodeSourceNotFound, "source not found")
	}
	return sourceFromORM(source), nil
}

func (r *SQLRepository) GetSourceByDatasetID(ctx context.Context, datasetID string) (Source, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return Source{}, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return Source{}, NewStoreError(ErrCodeSourceNotFound, "source not found")
	}
	var source ormSource
	if err := db.Where("dataset_id = ? AND deleted_at IS NULL", datasetID).First(&source).Error; err != nil {
		return Source{}, mapORMNotFound(err, ErrCodeSourceNotFound, "source not found")
	}
	return sourceFromORM(source), nil
}

func (r *SQLRepository) ListSourceAccess(ctx context.Context, tenantID string) ([]Source, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return nil, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var rows []ormSource
	query := db.Where("deleted_at IS NULL")
	if tenantID != "" {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if err := query.Order("source_id").Find(&rows).Error; err != nil {
		return nil, mapSQLConstraint(err)
	}
	sources := []Source{}
	for _, row := range rows {
		sources = append(sources, sourceFromORM(row))
	}
	return sources, nil
}

func (r *SQLRepository) UpdateSource(ctx context.Context, source Source) error {
	db := r.ormDB(ctx)
	if db == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	return ormUpdateSource(db, source)
}

func (r *SQLRepository) UpdateSourceWithBindings(ctx context.Context, mutation SourceUpdateMutation) (SourceUpdateResult, error) {
	var result SourceUpdateResult
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var current ormSource
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ?", mutation.Source.SourceID).First(&current).Error; err != nil {
			return mapORMNotFound(err, ErrCodeSourceNotFound, "source not found")
		}
		if err := ormUpdateSource(tx, mutation.Source); err != nil {
			return err
		}
		if err := releaseCurrentBindingTargets(ctx, tx, mutation, mutation.Now); err != nil {
			return err
		}
		for _, item := range mutation.DeleteBindings {
			binding, cleanup, err := r.softDeleteBindingTx(ctx, tx, item.SourceID, item.BindingID, item.DeletedAt)
			if err != nil {
				return err
			}
			_ = binding
			result.Cleanup.Add(cleanup)
		}
		for _, item := range mutation.UpdateBindings {
			if err := ormUpdateBinding(tx, item.Binding); err != nil {
				return err
			}
			if err := ormUpsertCheckpoint(tx, item.Checkpoint); err != nil {
				return err
			}
			if item.Cleanup.CancelPendingScheduled {
				count, err := cancelPendingScheduledSyncRunsORMTx(tx, item.Binding.SourceID, item.Binding.BindingID, item.Binding.BindingGeneration, item.Cleanup.Reason, mutation.Now)
				if err != nil {
					return err
				}
				result.Cleanup.CancelledSyncRunCount += count
			}
			if item.Cleanup.ClearIndexedState {
				cleanup, err := cleanupBindingGenerationORMTx(tx, item.Binding.SourceID, item.Binding.BindingID, item.Cleanup.OldBindingGeneration, item.Cleanup.Reason, mutation.Now)
				if err != nil {
					return err
				}
				result.Cleanup.Add(cleanup)
			}
		}
		for _, item := range mutation.CreateBindings {
			if err := ormInsertBinding(tx, item.Binding); err != nil {
				return err
			}
			if err := ormUpsertCheckpoint(tx, item.Checkpoint); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func releaseCurrentBindingTargets(ctx context.Context, tx *gorm.DB, mutation SourceUpdateMutation, now time.Time) error {
	releaseIDs := map[string]struct{}{}
	for _, item := range mutation.DeleteBindings {
		releaseIDs[item.BindingID] = struct{}{}
	}
	for _, item := range mutation.UpdateBindings {
		releaseIDs[item.Binding.BindingID] = struct{}{}
	}
	for bindingID := range releaseIDs {
		var row ormBinding
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND binding_id = ?", mutation.Source.SourceID, bindingID).First(&row).Error; err != nil {
			return mapORMNotFound(err, ErrCodeBindingNotFound, "binding not found")
		}
		binding := bindingFromORM(row)
		binding.Status = "DELETING"
		binding.DeletedAt = &now
		binding.UpdatedAt = now
		if err := ormUpdateBinding(tx, binding); err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLRepository) DeleteSource(ctx context.Context, sourceID string, deletedAt time.Time) (SourceDeleteResult, error) {
	var result SourceDeleteResult
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var row ormSource
		if err := tx.Where("source_id = ?", sourceID).First(&row).Error; err != nil {
			return mapORMNotFound(err, ErrCodeSourceNotFound, "source not found")
		}
		src := sourceFromORM(row)
		src.Status = "DELETING"
		src.DeletedAt = &deletedAt
		src.UpdatedAt = deletedAt
		if err := ormUpdateSource(tx, src); err != nil {
			return err
		}
		result.Source = src
		var bindings []ormBinding
		if err := tx.Where("source_id = ?", sourceID).Find(&bindings).Error; err != nil {
			return mapSQLConstraint(err)
		}
		for _, row := range bindings {
			binding := bindingFromORM(row)
			deleted, cleanup, err := r.softDeleteBindingTx(ctx, tx, binding.SourceID, binding.BindingID, deletedAt)
			if err != nil {
				return err
			}
			result.Bindings = append(result.Bindings, deleted)
			result.Cleanup.Add(cleanup)
		}
		return nil
	})
	return result, err
}

func mapORMNotFound(err error, code ErrorCode, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NewStoreError(code, message)
	}
	return mapSQLConstraint(err)
}

func applySourceListFilters(db *gorm.DB, req SourceListRequest) *gorm.DB {
	if req.TenantID != "" {
		db = db.Where("s.tenant_id = ?", req.TenantID)
	}
	if req.SourceIDs != nil {
		if len(req.SourceIDs) == 0 {
			db = db.Where("1 = 0")
		} else {
			db = db.Where("s.source_id IN ?", req.SourceIDs)
		}
	}
	if req.Status != "" {
		db = db.Where("s.status = ?", req.Status)
	}
	if strings.TrimSpace(req.Keyword) != "" {
		db = db.Where("LOWER(s.name) LIKE ?", "%"+strings.ToLower(strings.TrimSpace(req.Keyword))+"%")
	}
	return db
}

func ormInsertSource(db *gorm.DB, source Source) error {
	err := db.Table("sources").Create(sourceCreateValues(source)).Error
	return mapSQLConstraint(err)
}

func ormUpdateSource(db *gorm.DB, source Source) error {
	err := db.Model(&ormSource{}).Where("source_id = ?", source.SourceID).Updates(sourceUpdateValues(source)).Error
	return mapSQLConstraint(err)
}

func sourceCreateValues(source Source) map[string]any {
	values := sourceUpdateValues(source)
	values["source_id"] = source.SourceID
	values["created_at"] = source.CreatedAt
	return values
}

func sourceUpdateValues(source Source) map[string]any {
	return map[string]any{
		"tenant_id":               nullString(source.TenantID),
		"created_by":              source.CreatedBy,
		"name":                    source.Name,
		"dataset_id":              source.DatasetID,
		"status":                  source.Status,
		"source_options_json":     source.SourceOptions,
		"include_extensions_json": source.IncludeExtensions,
		"exclude_extensions_json": source.ExcludeExtensions,
		"config_version":          source.ConfigVersion,
		"deleted_at":              source.DeletedAt,
		"updated_at":              source.UpdatedAt,
	}
}

func ormUpsertOperation(db *gorm.DB, operation CreateOperation) error {
	err := db.Table("data_source_create_operations").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "caller_id"}, {Name: "request_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"source_id", "dataset_id", "created_core_parent_document_ids_json", "created_binding_ids_json",
			"warning_json", "status", "compensation_status", "compensation_error", "updated_at",
		}),
	}).Create(map[string]any{
		"operation_id":                          operation.OperationID,
		"caller_id":                             operation.CallerID,
		"request_id":                            operation.RequestID,
		"request_hash":                          operation.RequestHash,
		"source_id":                             nullString(operation.SourceID),
		"dataset_id":                            nullString(operation.DatasetID),
		"created_core_parent_document_ids_json": operation.CreatedCoreParentDocumentIDs,
		"created_binding_ids_json":              operation.CreatedBindingIDs,
		"warning_json":                          operation.Warning,
		"status":                                operation.Status,
		"compensation_status":                   operation.CompensationStatus,
		"compensation_error":                    operation.CompensationError,
		"created_at":                            operation.CreatedAt,
		"updated_at":                            operation.UpdatedAt,
	}).Error
	return mapSQLConstraint(err)
}
