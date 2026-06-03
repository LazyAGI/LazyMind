package source

import "context"

func (e *DefaultEngine) DeleteSource(ctx context.Context, sourceID string) (DeleteSourceResponse, error) {
	now := e.clock().UTC()
	deleted, err := e.repo.DeleteSource(ctx, sourceID, now)
	if err != nil {
		return DeleteSourceResponse{}, mapStoreError(err)
	}
	var warnings []JobError
	removedBindingIDs := make([]string, 0, len(deleted.Bindings))
	for _, binding := range deleted.Bindings {
		removedBindingIDs = append(removedBindingIDs, binding.BindingID)
		warnings = append(warnings, e.deleteFolderAsWarning(ctx, deleted.Source.DatasetID, binding.CoreParentDocumentID, deleted.Source.CreatedBy)...)
	}
	if err := e.deleteCoreDataset(ctx, deleted.Source.DatasetID, deleted.Source.CreatedBy); err != nil {
		warnings = append(warnings, JobError{Code: string(ErrCodeInternal), Message: err.Error(), Details: map[string]any{"dataset_id": deleted.Source.DatasetID}})
	}
	return DeleteSourceResponse{
		Deleted:            true,
		SourceID:           deleted.Source.SourceID,
		RemovedBindingIDs:  removedBindingIDs,
		RemovedDatasetID:   deleted.Source.DatasetID,
		CompensationErrors: warnings,
	}, nil
}
