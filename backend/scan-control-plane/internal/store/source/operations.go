package source

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

func (r *SQLRepository) GetCreateOperation(ctx context.Context, callerID, requestID string) (CreateOperation, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return CreateOperation{}, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var row ormCreateOperation
	err := db.Select(`operation_id, caller_id, request_id, request_hash,
COALESCE(source_id, '') AS source_id, COALESCE(dataset_id, '') AS dataset_id,
created_core_parent_document_ids_json, created_binding_ids_json, warning_json, status,
compensation_status, compensation_error, created_at, updated_at`).
		Where("caller_id = ? AND request_id = ?", callerID, requestID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CreateOperation{}, NewStoreError(ErrCodeNotFound, "operation not found")
		}
		return CreateOperation{}, mapSQLConstraint(err)
	}
	return createOperationFromORM(row), nil
}

func (r *SQLRepository) SaveCreateOperation(ctx context.Context, operation CreateOperation) error {
	db := r.ormDB(ctx)
	if db == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	return mapSQLConstraint(db.Model(&ormCreateOperation{}).Create(createOperationAttrs(operation)).Error)
}

func (r *SQLRepository) UpdateCreateOperation(ctx context.Context, operation CreateOperation) error {
	db := r.ormDB(ctx)
	if db == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	result := db.Model(&ormCreateOperation{}).
		Where("caller_id = ? AND request_id = ?", operation.CallerID, operation.RequestID).
		Updates(createOperationUpdateAttrs(operation))
	if result.Error != nil {
		return mapSQLConstraint(result.Error)
	}
	if result.RowsAffected == 0 {
		return NewStoreError(ErrCodeNotFound, "operation not found")
	}
	return nil
}

func createOperationFromORM(row ormCreateOperation) CreateOperation {
	return CreateOperation{
		OperationID:                  row.OperationID,
		CallerID:                     row.CallerID,
		RequestID:                    row.RequestID,
		RequestHash:                  row.RequestHash,
		SourceID:                     row.SourceID,
		DatasetID:                    row.DatasetID,
		CreatedCoreParentDocumentIDs: row.CreatedCoreParentDocumentIDs,
		CreatedBindingIDs:            row.CreatedBindingIDs,
		Warning:                      row.Warning,
		Status:                       row.Status,
		CompensationStatus:           row.CompensationStatus,
		CompensationError:            row.CompensationError,
		CreatedAt:                    row.CreatedAt,
		UpdatedAt:                    row.UpdatedAt,
	}
}

func createOperationAttrs(operation CreateOperation) map[string]any {
	attrs := createOperationUpdateAttrs(operation)
	attrs["operation_id"] = operation.OperationID
	attrs["caller_id"] = operation.CallerID
	attrs["request_id"] = operation.RequestID
	attrs["request_hash"] = operation.RequestHash
	attrs["created_at"] = operation.CreatedAt
	return attrs
}

func createOperationUpdateAttrs(operation CreateOperation) map[string]any {
	return map[string]any{
		"source_id":                             nullString(operation.SourceID),
		"dataset_id":                            nullString(operation.DatasetID),
		"created_core_parent_document_ids_json": operation.CreatedCoreParentDocumentIDs,
		"created_binding_ids_json":              operation.CreatedBindingIDs,
		"warning_json":                          operation.Warning,
		"status":                                operation.Status,
		"compensation_status":                   operation.CompensationStatus,
		"compensation_error":                    operation.CompensationError,
		"updated_at":                            operation.UpdatedAt,
	}
}
