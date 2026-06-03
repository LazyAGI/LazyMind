package source

import (
	"context"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *SQLRepository) ListDeletingResources(ctx context.Context, req AdminListRequest) ([]DeletingResource, int, error) {
	pageSize := normalizeSQLPageSize(req.PageSize)
	offset := parseAdminOffset(req.Page, req.PageSize)
	db, err := r.sourceORM(ctx)
	if err != nil {
		return nil, 0, err
	}

	items := []DeletingResource{}
	if req.SourceIDs != nil && len(req.SourceIDs) == 0 {
		return items, 0, nil
	}

	if req.BindingID == "" {
		var sources []ormSource
		query := db.Model(&ormSource{}).Where("status = ?", "DELETING")
		query = applyStringFilter(query, "source_id", req.SourceID)
		query = applyStringSetFilter(query, "source_id", req.SourceIDs)
		if err := query.Find(&sources).Error; err != nil {
			return nil, 0, mapSQLConstraint(err)
		}
		for _, source := range sources {
			items = append(items, DeletingResource{
				ResourceType: "source",
				SourceID:     source.SourceID,
				Status:       source.Status,
				DeletedAt:    source.DeletedAt,
				UpdatedAt:    source.UpdatedAt,
			})
		}
	}

	var bindings []ormBinding
	query := db.Model(&ormBinding{}).Where("status = ?", "DELETING")
	query = applyStringFilter(query, "source_id", req.SourceID)
	query = applyStringFilter(query, "binding_id", req.BindingID)
	query = applyStringSetFilter(query, "source_id", req.SourceIDs)
	if err := query.Find(&bindings).Error; err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	for _, binding := range bindings {
		items = append(items, DeletingResource{
			ResourceType: "binding",
			SourceID:     binding.SourceID,
			BindingID:    binding.BindingID,
			Status:       binding.Status,
			DeletedAt:    binding.DeletedAt,
			LastError:    binding.LastError,
			UpdatedAt:    binding.UpdatedAt,
		})
	}

	sortDeletingResources(items)
	total := len(items)
	return pageDeletingResources(items, offset, pageSize), total, nil
}

func (r *SQLRepository) ListFailedCreateOperations(ctx context.Context, req CreateOperationListRequest) ([]CreateOperation, int, error) {
	pageSize := normalizeSQLPageSize(req.PageSize)
	offset := parseAdminOffset(req.Page, req.PageSize)
	db, err := r.sourceORM(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := createOperationListBaseQuery(db, req)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	var rows []ormCreateOperation
	err = createOperationListBaseQuery(db, req).
		Select(createOperationORMSelect()).
		Order("updated_at DESC, operation_id").
		Limit(pageSize).
		Offset(offset).
		Find(&rows).Error
	if err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	items := make([]CreateOperation, 0, len(rows))
	for _, row := range rows {
		items = append(items, createOperationFromORM(row))
	}
	return items, int(total), nil
}

func (r *SQLRepository) ClaimCreateOperationCompensation(ctx context.Context, operationID string) (CreateOperation, error) {
	var op CreateOperation
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var row ormCreateOperation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select(createOperationORMSelect()).
			Where("operation_id = ?", operationID).
			First(&row).Error
		if err != nil {
			return mapGORMError(err, ErrCodeNotFound, "operation not found")
		}
		current := createOperationFromORM(row)
		if current.Status != "FAILED" || current.CompensationStatus != "FAILED" {
			return NewStoreError(ErrCodeGenerationConflict, "operation compensation is not retryable")
		}
		current.CompensationStatus = "RUNNING"
		current.CompensationError = nil
		if err := updateCreateOperationByIDORM(tx, current); err != nil {
			return err
		}
		op = current
		return nil
	})
	return op, err
}

func (r *SQLRepository) UpdateCreateOperationByID(ctx context.Context, operation CreateOperation) error {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return err
	}
	return updateCreateOperationByIDORM(db, operation)
}

func (r *SQLRepository) ListDeadLetters(ctx context.Context, req DeadLetterListRequest) ([]ParseTaskDeadLetter, int, error) {
	pageSize := normalizeSQLPageSize(req.PageSize)
	offset := parseAdminOffset(req.Page, req.PageSize)
	db, err := r.sourceORM(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := deadLetterListBaseQuery(db, req)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	var rows []ormParseTaskDeadLetter
	err = deadLetterListBaseQuery(db, req).
		Select(parseTaskDeadLetterORMSelect()).
		Order("failed_at DESC, dead_letter_id").
		Limit(pageSize).
		Offset(offset).
		Find(&rows).Error
	if err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	items := make([]ParseTaskDeadLetter, 0, len(rows))
	for _, row := range rows {
		items = append(items, parseTaskDeadLetterFromORM(row))
	}
	return items, int(total), nil
}

func (r *SQLRepository) GetDeadLetter(ctx context.Context, deadLetterID string) (ParseTaskDeadLetter, error) {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return ParseTaskDeadLetter{}, err
	}
	var row ormParseTaskDeadLetter
	err = db.Select(parseTaskDeadLetterORMSelect()).Where("dead_letter_id = ?", deadLetterID).First(&row).Error
	if err != nil {
		return ParseTaskDeadLetter{}, mapGORMError(err, ErrCodeNotFound, "dead letter not found")
	}
	return parseTaskDeadLetterFromORM(row), nil
}

func (r *SQLRepository) DeleteDeadLetter(ctx context.Context, deadLetterID string) error {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return err
	}
	result := db.Where("dead_letter_id = ?", deadLetterID).Delete(&ormParseTaskDeadLetter{})
	if result.Error != nil {
		return mapSQLConstraint(result.Error)
	}
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeNotFound, "dead letter not found")
	}
	return nil
}

func (r *SQLRepository) EnqueueBindingReconcile(ctx context.Context, req ReconcileRequest) (ReconcileResult, error) {
	binding, err := r.GetBinding(ctx, req.SourceID, req.BindingID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if binding.Status != "ACTIVE" {
		return ReconcileResult{}, NewStoreError(ErrCodeGenerationConflict, "binding is not active")
	}
	runID := req.RequestID
	if strings.TrimSpace(runID) == "" {
		runID = "reconcile-" + req.BindingID + "-" + req.RunAt.UTC().Format("20060102150405")
	}
	run := SyncRun{
		RunID:             runID,
		SourceID:          req.SourceID,
		BindingID:         req.BindingID,
		BindingGeneration: binding.BindingGeneration,
		TriggerType:       "reconcile",
		ScopeType:         "full",
		Coverage:          JSON{},
		Status:            SyncRunStatusPending,
		StartedAt:         req.RunAt,
	}
	persisted, _, err := r.EnqueueSyncRun(ctx, run)
	return ReconcileResult{Run: persisted}, err
}

func createOperationListBaseQuery(db *gorm.DB, req CreateOperationListRequest) *gorm.DB {
	query := db.Model(&ormCreateOperation{})
	query = applyStringSetFilter(query, "source_id", req.SourceIDs)
	query = applyStringSetFilter(query, "status", req.Statuses)
	query = applyStringSetFilter(query, "compensation_status", req.CompensationStatuses)
	return query
}

func deadLetterListBaseQuery(db *gorm.DB, req DeadLetterListRequest) *gorm.DB {
	query := db.Model(&ormParseTaskDeadLetter{})
	query = applyStringSetFilter(query, "source_id", req.SourceIDs)
	query = applyStringFilter(query, "source_id", req.SourceID)
	query = applyStringFilter(query, "binding_id", req.BindingID)
	return query
}

func applyStringFilter(db *gorm.DB, column, value string) *gorm.DB {
	if value == "" {
		return db
	}
	return db.Where(column+" = ?", value)
}

func applyStringSetFilter(db *gorm.DB, column string, values []string) *gorm.DB {
	if values == nil {
		return db
	}
	if len(values) == 0 {
		return db.Where("1 = 0")
	}
	return db.Where(column+" IN ?", values)
}

func sortDeletingResources(items []DeletingResource) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if left.ResourceType != right.ResourceType {
			return left.ResourceType < right.ResourceType
		}
		if left.SourceID != right.SourceID {
			return left.SourceID < right.SourceID
		}
		return left.BindingID < right.BindingID
	})
}

func pageDeletingResources(items []DeletingResource, offset, pageSize int) []DeletingResource {
	if offset >= len(items) {
		return []DeletingResource{}
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

func createOperationORMSelect() string {
	return `operation_id, caller_id, request_id, request_hash,
COALESCE(source_id, '') AS source_id, COALESCE(dataset_id, '') AS dataset_id,
created_core_parent_document_ids_json, created_binding_ids_json, warning_json, status,
compensation_status, compensation_error, created_at, updated_at`
}

func updateCreateOperationByIDORM(db *gorm.DB, operation CreateOperation) error {
	attrs := createOperationUpdateAttrs(operation)
	attrs["updated_at"] = gorm.Expr("now()")
	result := db.Model(&ormCreateOperation{}).
		Where("operation_id = ?", operation.OperationID).
		Updates(attrs)
	if result.Error != nil {
		return mapSQLConstraint(result.Error)
	}
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeNotFound, "operation not found")
	}
	return nil
}

func parseTaskDeadLetterORMSelect() string {
	return `dead_letter_id, task_id, COALESCE(tenant_id, '') AS tenant_id, source_id, binding_id, binding_generation,
object_key, document_id, task_action, target_version_id, retry_count, COALESCE(error_code, '') AS error_code,
last_error, failed_at, created_at`
}

func parseTaskDeadLetterFromORM(row ormParseTaskDeadLetter) ParseTaskDeadLetter {
	return ParseTaskDeadLetter{
		DeadLetterID:      row.DeadLetterID,
		TaskID:            row.TaskID,
		TenantID:          row.TenantID,
		SourceID:          row.SourceID,
		BindingID:         row.BindingID,
		BindingGeneration: row.BindingGeneration,
		ObjectKey:         row.ObjectKey,
		DocumentID:        row.DocumentID,
		TaskAction:        row.TaskAction,
		TargetVersionID:   row.TargetVersionID,
		RetryCount:        row.RetryCount,
		ErrorCode:         row.ErrorCode,
		LastError:         row.LastError,
		FailedAt:          row.FailedAt,
		CreatedAt:         row.CreatedAt,
	}
}

func parseAdminOffset(page, pageSize int) int {
	page, pageSize = normalizeSQLPage(page, pageSize)
	return (page - 1) * pageSize
}
