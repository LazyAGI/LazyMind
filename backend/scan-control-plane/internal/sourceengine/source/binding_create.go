package source

import (
	"context"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DefaultEngine) AddBinding(ctx context.Context, callerID, sourceID string, input BindingInput) (BindingMutationResponse, error) {
	src, err := e.repo.GetSource(ctx, sourceID)
	if err != nil {
		return BindingMutationResponse{}, mapStoreError(err)
	}
	now := e.clock().UTC()
	prepared, err := e.prepareCreateBinding(ctx, sourceID, src.DatasetID, src.Name, callerID, src.TenantID, "", 0, input, now)
	if err != nil {
		return BindingMutationResponse{}, err
	}
	if err := e.ensureUniqueTarget(ctx, prepared.binding, ""); err != nil {
		_ = e.deleteCoreFolder(ctx, src.DatasetID, prepared.binding.CoreParentDocumentID, callerID)
		return BindingMutationResponse{}, err
	}
	if err := mapStoreError(e.repo.AddBinding(ctx, prepared.binding, prepared.checkpoint)); err != nil {
		_ = e.deleteCoreFolder(ctx, src.DatasetID, prepared.binding.CoreParentDocumentID, callerID)
		return BindingMutationResponse{}, err
	}
	jobIDs, jobErrors := e.triggerInitialSyncs(ctx, []store.Binding{prepared.binding})
	return BindingMutationResponse{Binding: bindingToResponse(prepared.binding), NewGeneration: prepared.binding.BindingGeneration, JobIDs: jobIDs, CompensationErrors: jobErrors}, nil
}

func (e *DefaultEngine) ensureUniqueTarget(ctx context.Context, binding store.Binding, excludeBindingID string) error {
	conflict, ok, err := e.repo.FindActiveBindingByTarget(ctx, binding.SourceID, excludeBindingID, binding.ConnectorType, binding.TargetType, binding.TargetFingerprint)
	if err != nil {
		return mapStoreError(err)
	}
	if ok {
		return &EngineError{
			Code:    ErrCodeBindingTargetDuplicated,
			Message: "binding target is already used by this source",
			Details: map[string]any{
				"binding_id":         conflict.BindingID,
				"target_fingerprint": binding.TargetFingerprint,
			},
		}
	}
	return nil
}
