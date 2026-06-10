package source

import (
	"context"
	"time"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DefaultEngine) UpdateSource(ctx context.Context, callerID, sourceID string, req UpdateSourceRequest) (UpdateSourceResponse, error) {
	src, err := e.repo.GetSource(ctx, sourceID)
	if err != nil {
		return UpdateSourceResponse{}, mapStoreError(err)
	}
	if src.ConfigVersion != req.ConfigVersion {
		return UpdateSourceResponse{}, NewError(ErrCodeSourceVersionConflict, "source config_version does not match")
	}
	now := e.clock().UTC()
	if req.Name != nil {
		if err := validateSourceName(*req.Name); err != nil {
			return UpdateSourceResponse{}, err
		}
		src.Name = *req.Name
	}
	if req.IncludeExtensions != nil {
		src.IncludeExtensions = jsonFromStrings(req.IncludeExtensions)
	}
	if req.ExcludeExtensions != nil {
		src.ExcludeExtensions = jsonFromStrings(req.ExcludeExtensions)
	}
	if req.SourceOptions != nil {
		src.SourceOptions = jsonFromMap(req.SourceOptions)
	}
	src.ConfigVersion++
	src.UpdatedAt = now

	changes := bindingListChanges{callerID: callerID}
	if req.BindingsProvided {
		changes, err = e.prepareBindingList(ctx, callerID, src, req.Bindings, now)
		if err != nil {
			return UpdateSourceResponse{}, err
		}
		if _, err := e.repo.UpdateSourceWithBindings(ctx, changes.mutation(src, now)); err != nil {
			compensatePreparedMutations(ctx, e, changes)
			return UpdateSourceResponse{}, mapStoreError(err)
		}
	} else if err := mapStoreError(e.repo.UpdateSource(ctx, src)); err != nil {
		return UpdateSourceResponse{}, err
	}
	result := UpdateSourceResponse{
		Source:            sourceToResponse(src),
		CreatedBindingIDs: changes.created,
		UpdatedBindingIDs: changes.updated,
		RemovedBindingIDs: changes.removed,
	}
	result.JobIDs, result.JobErrors = e.runPostCommitBindingActions(ctx, changes)
	bindings, err := e.repo.ListBindings(ctx, sourceID)
	if err != nil {
		return UpdateSourceResponse{}, mapStoreError(err)
	}
	result.Bindings = bindingsToResponse(bindings)
	return result, nil
}

type bindingListChanges struct {
	callerID        string
	datasetID       string
	created         []string
	updated         []string
	removed         []string
	createdBindings []preparedBinding
	updatedBindings []store.BindingUpdateMutation
	deletedBindings []store.BindingDeleteMutation
	oldFolderIDs    []string
	initialSyncs    []store.Binding
}

func (e *DefaultEngine) prepareBindingList(ctx context.Context, callerID string, src store.Source, inputs []BindingInput, now time.Time) (bindingListChanges, error) {
	existing, err := e.repo.ListBindings(ctx, src.SourceID)
	if err != nil {
		return bindingListChanges{}, mapStoreError(err)
	}
	existingByID := bindingByID(existing)
	seen := make(map[string]struct{}, len(inputs))
	changes := bindingListChanges{callerID: callerID, datasetID: src.DatasetID}
	for _, input := range inputs {
		if input.BindingID == "" {
			prepared, err := e.prepareCreateBinding(ctx, src.SourceID, src.DatasetID, src.Name, callerID, src.TenantID, "", len(changes.createdBindings), input, now)
			if err != nil {
				compensatePreparedCreates(ctx, e, changes.datasetID, changes.callerID, changes.createdBindings)
				return bindingListChanges{}, err
			}
			changes.created = append(changes.created, prepared.binding.BindingID)
			changes.createdBindings = append(changes.createdBindings, prepared)
			changes.initialSyncs = append(changes.initialSyncs, prepared.binding)
			seen[prepared.binding.BindingID] = struct{}{}
			continue
		}
		current, ok := existingByID[input.BindingID]
		if !ok {
			compensatePreparedCreates(ctx, e, changes.datasetID, changes.callerID, changes.createdBindings)
			return bindingListChanges{}, NewError(ErrCodeBindingNotFound, "binding does not belong to source")
		}
		updated, checkpoint, cleanup, err := e.prepareUpdateBinding(ctx, callerID, src, current, input)
		if err != nil {
			compensatePreparedMutations(ctx, e, changes)
			return bindingListChanges{}, err
		}
		seen[input.BindingID] = struct{}{}
		changes.updated = append(changes.updated, input.BindingID)
		changes.updatedBindings = append(changes.updatedBindings, store.BindingUpdateMutation{Binding: updated, Checkpoint: checkpoint, Cleanup: cleanup})
		if cleanup.ClearIndexedState {
			changes.oldFolderIDs = append(changes.oldFolderIDs, cleanup.OldCoreParentDocumentID)
			changes.initialSyncs = append(changes.initialSyncs, updated)
		}
	}
	for _, binding := range existing {
		if binding.Status == BindingStatusDeleting {
			continue
		}
		if _, ok := seen[binding.BindingID]; ok {
			continue
		}
		changes.removed = append(changes.removed, binding.BindingID)
		changes.deletedBindings = append(changes.deletedBindings, store.BindingDeleteMutation{SourceID: binding.SourceID, BindingID: binding.BindingID, DeletedAt: now})
		changes.oldFolderIDs = append(changes.oldFolderIDs, binding.CoreParentDocumentID)
	}
	if err := ensureFinalTargetsUnique(existing, changes); err != nil {
		compensatePreparedMutations(ctx, e, changes)
		return bindingListChanges{}, err
	}
	return changes, nil
}

func (c bindingListChanges) mutation(src store.Source, now time.Time) store.SourceUpdateMutation {
	mutation := store.SourceUpdateMutation{Source: src, UpdateBindings: c.updatedBindings, DeleteBindings: c.deletedBindings, Now: now}
	for _, item := range c.createdBindings {
		mutation.CreateBindings = append(mutation.CreateBindings, store.BindingCreateMutation{Binding: item.binding, Checkpoint: item.checkpoint})
	}
	return mutation
}

func (e *DefaultEngine) runPostCommitBindingActions(ctx context.Context, changes bindingListChanges) ([]string, []JobError) {
	var jobErrors []JobError
	for _, folderID := range changes.oldFolderIDs {
		jobErrors = append(jobErrors, e.deleteFolderAsWarning(ctx, changes.datasetID, folderID, changes.callerID)...)
	}
	jobIDs, syncErrors := e.triggerInitialSyncs(ctx, changes.initialSyncs)
	jobErrors = append(jobErrors, syncErrors...)
	return jobIDs, jobErrors
}

func compensatePreparedCreates(ctx context.Context, e *DefaultEngine, datasetID, callerID string, bindings []preparedBinding) {
	for _, item := range bindings {
		_ = e.deleteCoreFolder(ctx, datasetID, item.binding.CoreParentDocumentID, callerID)
	}
}

func compensatePreparedMutations(ctx context.Context, e *DefaultEngine, changes bindingListChanges) {
	compensatePreparedCreates(ctx, e, changes.datasetID, changes.callerID, changes.createdBindings)
	for _, item := range changes.updatedBindings {
		if item.Cleanup.ClearIndexedState {
			_ = e.deleteCoreFolder(ctx, changes.datasetID, item.Binding.CoreParentDocumentID, changes.callerID)
		}
	}
}

func ensureFinalTargetsUnique(existing []store.Binding, changes bindingListChanges) error {
	final := make([]store.Binding, 0, len(existing)+len(changes.createdBindings))
	updated := updatedBindingSet(changes.updatedBindings)
	deleted := deletedBindingSet(changes.deletedBindings)
	for _, binding := range existing {
		if binding.Status == BindingStatusDeleting {
			continue
		}
		if next, ok := updated[binding.BindingID]; ok {
			if next.Status != BindingStatusDeleting {
				final = append(final, next)
			}
			continue
		}
		if _, ok := deleted[binding.BindingID]; !ok {
			final = append(final, binding)
		}
	}
	for _, item := range changes.createdBindings {
		final = append(final, item.binding)
	}
	seen := map[string]store.Binding{}
	for _, binding := range final {
		key := bindingTargetKey(binding)
		if conflict, ok := seen[key]; ok {
			return duplicatedTargetError(conflict, binding.TargetFingerprint)
		}
		seen[key] = binding
	}
	return nil
}

func updatedBindingSet(updates []store.BindingUpdateMutation) map[string]store.Binding {
	out := make(map[string]store.Binding, len(updates))
	for _, item := range updates {
		out[item.Binding.BindingID] = item.Binding
	}
	return out
}

func deletedBindingSet(deletes []store.BindingDeleteMutation) map[string]struct{} {
	out := make(map[string]struct{}, len(deletes))
	for _, item := range deletes {
		out[item.BindingID] = struct{}{}
	}
	return out
}

func bindingTargetKey(binding store.Binding) string {
	return binding.SourceID + "\x00" + binding.ConnectorType + "\x00" + binding.TargetType + "\x00" + binding.TargetFingerprint
}

func duplicatedTargetError(binding store.Binding, fingerprint string) error {
	return &EngineError{
		Code:    ErrCodeBindingTargetDuplicated,
		Message: "binding target is already used by this source",
		Details: map[string]any{
			"binding_id":         binding.BindingID,
			"target_fingerprint": fingerprint,
		},
	}
}

func bindingByID(bindings []store.Binding) map[string]store.Binding {
	out := make(map[string]store.Binding, len(bindings))
	for _, binding := range bindings {
		out[binding.BindingID] = binding
	}
	return out
}
