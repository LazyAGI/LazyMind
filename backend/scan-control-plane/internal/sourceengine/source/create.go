package source

import (
	"context"
	"time"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DefaultEngine) CreateSource(ctx context.Context, req CreateSourceRequest) (CreateSourceResponse, error) {
	if err := validateCreateRequest(req); err != nil {
		return CreateSourceResponse{}, err
	}
	hash, err := createRequestHash(req)
	if err != nil {
		return CreateSourceResponse{}, err
	}
	if replay, ok, err := e.loadCreateReplay(ctx, req.CallerID, req.RequestID, hash); ok || err != nil {
		return replay, err
	}

	now := e.clock().UTC()
	operation := e.newCreateOperation(req.CallerID, req.RequestID, hash, now)
	if err := mapStoreError(e.repo.SaveCreateOperation(ctx, operation)); err != nil {
		return CreateSourceResponse{}, err
	}

	datasetID, err := e.createCoreDataset(ctx, req)
	if err != nil {
		if updateErr := mapStoreError(e.repo.UpdateCreateOperation(ctx, operationWithFailure(operation, err))); updateErr != nil {
			return CreateSourceResponse{}, updateErr
		}
		return CreateSourceResponse{}, err
	}
	sourceID := e.newID("source")
	prepared, err := e.prepareCreateBindings(ctx, sourceID, datasetID, req, now)
	if err != nil {
		if markErr := e.markCreateFailure(ctx, operation, datasetID, req.CallerID, prepared, err); markErr != nil {
			return CreateSourceResponse{}, markErr
		}
		return CreateSourceResponse{}, err
	}

	source := e.newSource(sourceID, datasetID, req, now)
	operation.SourceID = sourceID
	operation.DatasetID = datasetID
	operation.CreatedBindingIDs = bindingIDsJSON(prepared)
	operation.CreatedCoreParentDocumentIDs = folderIDsJSON(prepared)
	record := store.SourceCreateRecord{Source: source, Bindings: collectBindings(prepared), Checkpoints: collectCheckpoints(prepared), Operation: operation}
	if err := mapStoreError(e.repo.CreateSourceWithBindings(ctx, record)); err != nil {
		if markErr := e.markCreateFailure(ctx, operation, datasetID, req.CallerID, prepared, err); markErr != nil {
			return CreateSourceResponse{}, markErr
		}
		return CreateSourceResponse{}, err
	}

	operation.Status = OperationStatusSucceeded
	operation.CompensationStatus = CompensationStatusNone
	if err := mapStoreError(e.repo.UpdateCreateOperation(ctx, operation)); err != nil {
		return CreateSourceResponse{}, err
	}
	return CreateSourceResponse{Source: sourceToResponse(source), Bindings: bindingsToResponse(collectBindings(prepared))}, nil
}

func (e *DefaultEngine) loadCreateReplay(ctx context.Context, callerID, requestID, hash string) (CreateSourceResponse, bool, error) {
	operation, err := e.repo.GetCreateOperation(ctx, callerID, requestID)
	if err != nil {
		if store.ErrorCodeOf(err) == store.ErrCodeNotFound {
			return CreateSourceResponse{}, false, nil
		}
		return CreateSourceResponse{}, false, mapStoreError(err)
	}
	if operation.RequestHash != hash {
		return CreateSourceResponse{}, true, NewError(ErrCodeIdempotencyKeyReused, "request_id was reused with a different payload")
	}
	if operation.Status != OperationStatusSucceeded && operation.Status != OperationStatusSucceededWithWarning {
		return CreateSourceResponse{}, true, NewError(ErrCodeIdempotencyKeyReused, "request_id is already in progress")
	}
	return e.replayCreateOperation(ctx, operation)
}

func (e *DefaultEngine) replayCreateOperation(ctx context.Context, operation store.CreateOperation) (CreateSourceResponse, bool, error) {
	src, err := e.repo.GetSource(ctx, operation.SourceID)
	if err != nil {
		return CreateSourceResponse{}, true, mapStoreError(err)
	}
	bindings, err := e.repo.ListBindings(ctx, operation.SourceID)
	if err != nil {
		return CreateSourceResponse{}, true, mapStoreError(err)
	}
	return CreateSourceResponse{Source: sourceToResponse(src), Bindings: bindingsToResponse(bindings)}, true, nil
}

func (e *DefaultEngine) newCreateOperation(callerID, requestID, hash string, now time.Time) store.CreateOperation {
	return store.CreateOperation{
		OperationID:        e.newID("op"),
		CallerID:           callerID,
		RequestID:          requestID,
		RequestHash:        hash,
		Status:             OperationStatusPending,
		CompensationStatus: CompensationStatusNone,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func (e *DefaultEngine) newSource(sourceID, datasetID string, req CreateSourceRequest, now time.Time) store.Source {
	return store.Source{
		SourceID:          sourceID,
		TenantID:          req.TenantID,
		CreatedBy:         req.CallerID,
		Name:              req.Name,
		DatasetID:         datasetID,
		Status:            SourceStatusActive,
		SourceOptions:     jsonFromMap(req.SourceOptions),
		IncludeExtensions: jsonFromStrings(req.IncludeExtensions),
		ExcludeExtensions: jsonFromStrings(req.ExcludeExtensions),
		ConfigVersion:     1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func (e *DefaultEngine) prepareCreateBindings(ctx context.Context, sourceID, datasetID string, req CreateSourceRequest, now time.Time) ([]preparedBinding, error) {
	prepared := make([]preparedBinding, 0, len(req.Bindings))
	for index, input := range req.Bindings {
		item, err := e.prepareCreateBinding(ctx, sourceID, datasetID, req.CallerID, req.RequestID, index, input, now)
		if err != nil {
			return prepared, err
		}
		prepared = append(prepared, item)
	}
	if fingerprint, duplicated := duplicateInRequest(prepared); duplicated {
		return prepared, &EngineError{
			Code:    ErrCodeBindingTargetDuplicated,
			Message: "binding target is duplicated",
			Details: map[string]any{"target_fingerprint": fingerprint},
		}
	}
	return prepared, nil
}

func (e *DefaultEngine) markCreateFailure(ctx context.Context, operation store.CreateOperation, datasetID, callerID string, prepared []preparedBinding, cause error) error {
	folders := make([]string, 0, len(prepared))
	for _, item := range prepared {
		folders = append(folders, item.binding.CoreParentDocumentID)
	}
	warnings := e.compensateCreate(ctx, datasetID, callerID, folders)
	operation.Status = OperationStatusFailed
	if len(warnings) == 0 {
		operation.CompensationStatus = CompensationStatusSucceeded
	} else {
		operation.CompensationStatus = CompensationStatusFailed
		operation.CompensationError = jobErrorsJSON(warnings)
	}
	operation.Warning = store.JSON{"error": cause.Error()}
	return mapStoreError(e.repo.UpdateCreateOperation(ctx, operation))
}

func (e *DefaultEngine) triggerInitialSyncs(ctx context.Context, bindings []store.Binding) ([]string, []JobError) {
	var jobIDs []string
	var jobErrors []JobError
	for _, binding := range bindings {
		ids, err := e.schedule.TriggerInitialSync(ctx, binding)
		if err != nil {
			jobErr := JobError{Code: string(ErrCodeInternal), Message: err.Error(), Details: map[string]any{"binding_id": binding.BindingID}}
			if recordErr := e.recordSyncJobError(ctx, binding, jobErr); recordErr != nil {
				jobErr.Details["record_error"] = recordErr.Error()
			}
			jobErrors = append(jobErrors, jobErr)
			continue
		}
		jobIDs = append(jobIDs, ids...)
	}
	return jobIDs, jobErrors
}

func (e *DefaultEngine) recordSyncJobError(ctx context.Context, binding store.Binding, jobErr JobError) error {
	return mapStoreError(e.repo.RecordSyncJobError(ctx, binding.SourceID, binding.BindingID, binding.BindingGeneration, syncJobErrorJSON(jobErr), e.clock().UTC()))
}
