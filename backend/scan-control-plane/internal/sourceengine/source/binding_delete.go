package source

import (
	"context"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DefaultEngine) DeleteBinding(ctx context.Context, sourceID, bindingID string) (DeleteBindingResponse, error) {
	src, err := e.repo.GetSource(ctx, sourceID)
	if err != nil {
		return DeleteBindingResponse{}, mapStoreError(err)
	}
	now := e.clock().UTC()
	deleted, err := e.repo.DeleteBinding(ctx, sourceID, bindingID, now)
	if err != nil {
		return DeleteBindingResponse{}, mapStoreError(err)
	}
	warnings := e.deleteFolderAsWarning(ctx, src.DatasetID, deleted.Binding.CoreParentDocumentID, src.CreatedBy)
	warnings = append(warnings, e.queueLocalWatcherStops(ctx, src, []store.Binding{deleted.Binding})...)
	return DeleteBindingResponse{
		Deleted:                     true,
		BindingID:                   deleted.Binding.BindingID,
		RemovedCoreParentDocumentID: deleted.Binding.CoreParentDocumentID,
		CancelledTaskCount:          deleted.Cleanup.CancelledParseTaskCount,
		CompensationErrors:          warnings,
	}, nil
}
