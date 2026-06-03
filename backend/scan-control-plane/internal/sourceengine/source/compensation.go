package source

import (
	"context"
	"fmt"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DefaultEngine) compensateCreate(ctx context.Context, datasetID, callerID string, folders []string) []JobError {
	var errs []JobError
	for i := len(folders) - 1; i >= 0; i-- {
		if err := e.deleteCoreFolder(ctx, datasetID, folders[i], callerID); err != nil {
			errs = append(errs, JobError{Code: string(ErrCodeInternal), Message: err.Error(), Details: map[string]any{"core_parent_document_id": folders[i]}})
		}
	}
	if datasetID != "" {
		if err := e.deleteCoreDataset(ctx, datasetID, callerID); err != nil {
			errs = append(errs, JobError{Code: string(ErrCodeInternal), Message: err.Error(), Details: map[string]any{"dataset_id": datasetID}})
		}
	}
	return errs
}

func (e *DefaultEngine) deleteFolderAsWarning(ctx context.Context, datasetID, folderID, callerID string) []JobError {
	if folderID == "" {
		return nil
	}
	if err := e.deleteCoreFolder(ctx, datasetID, folderID, callerID); err != nil {
		return []JobError{{Code: string(ErrCodeInternal), Message: err.Error(), Details: map[string]any{"core_parent_document_id": folderID}}}
	}
	return nil
}

func (e *DefaultEngine) deleteCoreDataset(ctx context.Context, datasetID, callerID string) error {
	deleter, ok := e.core.(coreclient.DatasetDeletionClient)
	if !ok {
		return fmt.Errorf("core client does not support dataset deletion")
	}
	return deleter.DeleteDataset(ctx, coreclient.DeleteDatasetRequest{DatasetID: datasetID, UserID: callerID})
}

func operationWithFailure(operation store.CreateOperation, err error) store.CreateOperation {
	operation.Status = OperationStatusFailed
	operation.CompensationStatus = CompensationStatusSucceeded
	operation.CompensationError = store.JSON{"error": err.Error()}
	return operation
}
