package source

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DefaultEngine) UpdateBinding(ctx context.Context, callerID, sourceID, bindingID string, input BindingInput) (BindingMutationResponse, error) {
	src, err := e.repo.GetSource(ctx, sourceID)
	if err != nil {
		return BindingMutationResponse{}, mapStoreError(err)
	}
	current, err := e.repo.GetBinding(ctx, sourceID, bindingID)
	if err != nil {
		return BindingMutationResponse{}, mapStoreError(err)
	}
	updated, checkpoint, cleanup, err := e.prepareUpdateBinding(ctx, callerID, src, current, input)
	if err != nil {
		return BindingMutationResponse{}, err
	}
	if cleanup.ClearIndexedState {
		if err := e.ensureUniqueTarget(ctx, updated, bindingID); err != nil {
			_ = e.deleteCoreFolder(ctx, src.DatasetID, updated.CoreParentDocumentID, callerID)
			return BindingMutationResponse{}, err
		}
	}
	if err := mapStoreError(e.repo.UpdateBinding(ctx, updated, checkpoint, cleanup)); err != nil {
		if cleanup.ClearIndexedState {
			_ = e.deleteCoreFolder(ctx, src.DatasetID, updated.CoreParentDocumentID, callerID)
		}
		return BindingMutationResponse{}, err
	}
	warnings := e.deleteFolderAsWarning(ctx, src.DatasetID, cleanup.OldCoreParentDocumentID, callerID)
	warnings = append(warnings, e.queueLocalWatcherTransition(ctx, src, current, updated)...)
	return BindingMutationResponse{
		Binding:            bindingToResponse(updated),
		OldGeneration:      current.BindingGeneration,
		NewGeneration:      updated.BindingGeneration,
		CompensationErrors: warnings,
	}, nil
}

func (e *DefaultEngine) prepareUpdateBinding(ctx context.Context, callerID string, src store.Source, current store.Binding, input BindingInput) (store.Binding, store.SyncCheckpoint, store.BindingUpdateCleanup, error) {
	if input.SyncMode == "" {
		input.SyncMode = current.SyncMode
	}
	if input.SyncMode == SyncModeScheduled && input.SchedulePolicy == nil && current.SyncMode == SyncModeScheduled {
		input.SchedulePolicy = store.CloneJSON(current.SchedulePolicy)
	}
	if input.SyncMode != SyncModeScheduled {
		input.SchedulePolicy = nil
	}
	changedTarget := targetChanged(current, input)
	if changedTarget {
		input = completeTargetInput(current, input)
	}
	if input.ProviderOptions != nil {
		input.ProviderOptions = providerOptionsWithActor(input.ProviderOptions, callerID, src.TenantID)
	}
	if err := validateBindingInput(input, changedTarget); err != nil {
		return store.Binding{}, store.SyncCheckpoint{}, store.BindingUpdateCleanup{}, err
	}
	now := e.clock().UTC()
	updated := patchNonTargetFields(current, input, now)
	changedSchedule := scheduleChanged(current, updated)
	if changedSchedule {
		updated.NextSyncAt = nil
	}
	cleanup := store.BindingUpdateCleanup{}
	if changedTarget {
		target, err := e.validateTarget(ctx, callerID, input)
		if err != nil {
			return store.Binding{}, store.SyncCheckpoint{}, store.BindingUpdateCleanup{}, err
		}
		folderName := updated.CoreParentDocumentName
		if folderName == "" {
			folderName = bindingRootDisplayName(input.DisplayName, src.Name, target)
		}
		folderID, err := e.createCoreFolder(ctx, coreclient.CreateBindingRootDocumentRequest{
			IdempotencyKey: bindingFolderIdempotencyKey(current.BindingID, current.BindingGeneration+1),
			DatasetID:      src.DatasetID,
			Name:           folderName,
			UserID:         callerID,
		})
		if err != nil {
			return store.Binding{}, store.SyncCheckpoint{}, store.BindingUpdateCleanup{}, err
		}
		updated.CoreParentDocumentName = folderName
		cleanup = store.BindingUpdateCleanup{
			OldCoreParentDocumentID: current.CoreParentDocumentID,
			ClearIndexedState:       true,
			OldBindingGeneration:    current.BindingGeneration,
			Reason:                  "binding target changed",
		}
		updated = patchTargetFields(updated, input, target, folderID)
	} else if changedSchedule {
		cleanup = store.BindingUpdateCleanup{
			CancelPendingScheduled: true,
			Reason:                 "binding schedule changed",
		}
	}
	checkpoint, err := e.schedule.BuildCheckpoint(ctx, updated, now)
	if err != nil {
		if cleanup.ClearIndexedState {
			_ = e.deleteCoreFolder(ctx, src.DatasetID, updated.CoreParentDocumentID, callerID)
		}
		return store.Binding{}, store.SyncCheckpoint{}, store.BindingUpdateCleanup{}, err
	}
	updated.NextSyncAt = checkpoint.NextSyncAt
	return updated, checkpoint, cleanup, nil
}

func bindingFolderIdempotencyKey(bindingID string, generation int64) string {
	return fmt.Sprintf("binding:%s:generation:%d:folder", bindingID, generation)
}

func targetChanged(current store.Binding, input BindingInput) bool {
	if input.ConnectorType != "" && string(input.ConnectorType) != current.ConnectorType {
		return true
	}
	if input.TargetType != "" && string(input.TargetType) != current.TargetType {
		return true
	}
	if input.TargetRef != "" && input.TargetRef != current.TargetRef {
		return true
	}
	if input.AgentID != "" && input.AgentID != current.AgentID {
		return true
	}
	if input.AuthConnectionID != "" && input.AuthConnectionID != current.AuthConnectionID {
		return true
	}
	return false
}

func patchNonTargetFields(binding store.Binding, input BindingInput, now time.Time) store.Binding {
	if input.DisplayName != "" {
		binding.CoreParentDocumentName = input.DisplayName
	}
	if input.SyncMode != "" {
		binding.SyncMode = input.SyncMode
	}
	binding.SchedulePolicy = schedulePolicyForSyncMode(binding.SyncMode, input.SchedulePolicy)
	if input.IncludeExtensions != nil {
		binding.IncludeExtensions = jsonFromStrings(input.IncludeExtensions)
	}
	if input.ExcludeExtensions != nil {
		binding.ExcludeExtensions = jsonFromStrings(input.ExcludeExtensions)
	}
	if input.ProviderOptions != nil {
		binding.ProviderOptions = providerOptionsJSON(input.ProviderOptions)
	}
	if input.Status != "" {
		binding.Status = input.Status
	}
	binding.UpdatedAt = now
	return binding
}

func scheduleChanged(current, updated store.Binding) bool {
	if current.SyncMode != updated.SyncMode {
		return true
	}
	if current.SyncMode != SyncModeScheduled {
		return false
	}
	return !jsonEqual(current.SchedulePolicy, updated.SchedulePolicy)
}

func jsonEqual(left, right store.JSON) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return string(leftBody) == string(rightBody)
}

func patchTargetFields(binding store.Binding, input BindingInput, target connector.NormalizedTarget, folderID string) store.Binding {
	binding.ConnectorType = string(input.ConnectorType)
	binding.TargetType = string(target.TargetType)
	binding.TargetRef = target.TargetRef
	binding.TargetFingerprint = target.TargetFingerprint
	binding.AgentID = effectiveAgentID(input.AgentID, target)
	binding.AuthConnectionID = input.AuthConnectionID
	binding.ProviderOptions = providerOptionsJSON(input.ProviderOptions)
	binding.TreeKey = target.RootObjectKey
	binding.BindingGeneration++
	binding.CoreParentDocumentID = folderID
	if binding.CoreParentDocumentName == "" {
		binding.CoreParentDocumentName = target.DisplayName
	}
	return binding
}

func completeTargetInput(current store.Binding, input BindingInput) BindingInput {
	if input.ConnectorType == "" {
		input.ConnectorType = connector.ConnectorType(current.ConnectorType)
	}
	if input.TargetType == "" {
		input.TargetType = connector.TargetType(current.TargetType)
	}
	if input.TargetRef == "" {
		input.TargetRef = current.TargetRef
	}
	if input.AgentID == "" {
		input.AgentID = current.AgentID
	}
	if input.AuthConnectionID == "" {
		input.AuthConnectionID = current.AuthConnectionID
	}
	if input.ProviderOptions == nil {
		input.ProviderOptions = store.CloneJSON(current.ProviderOptions)
	}
	return input
}
