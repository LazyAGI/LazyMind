package state

import (
	"context"
	"slices"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/crawl"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type Store interface {
	GetDocumentState(ctx context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error)
	SaveDocumentState(ctx context.Context, state store.DocumentState) error
	ListDocumentStates(ctx context.Context, sourceID, bindingID string) ([]store.DocumentState, error)
	GetObject(ctx context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error)
	UpdateDocument(ctx context.Context, document store.Document) error
	GetDocument(ctx context.Context, sourceID, bindingID, objectKey string) (store.Document, error)
}

type stateMutationStore interface {
	MutateDocumentState(ctx context.Context, sourceID, bindingID, objectKey string, mutate store.DocumentStateMutation) (store.DocumentState, error)
}

type DBStateReducer struct {
	store Store
	clock func() time.Time
}

type Option func(*DBStateReducer)

func NewDBStateReducer(store Store, options ...Option) *DBStateReducer {
	r := &DBStateReducer{store: store, clock: time.Now}
	for _, option := range options {
		option(r)
	}
	return r
}

func WithClock(clock func() time.Time) Option {
	return func(r *DBStateReducer) {
		if clock != nil {
			r.clock = clock
		}
	}
}

func (r *DBStateReducer) ReduceSeenObjects(ctx context.Context, input crawl.ReduceSeenInput) (crawl.ReduceSeenResult, error) {
	result := crawl.ReduceSeenResult{}
	for _, object := range input.Objects {
		if !object.IsDocument {
			continue
		}
		next, isNew, err := r.mutateSeenState(ctx, input, object)
		if err != nil {
			return result, err
		}
		result.States = append(result.States, next)
		switch next.SourceState {
		case SourceStateNew:
			result.NewCount++
		case SourceStateModified:
			result.ModifiedCount++
		case SourceStateDeleted:
			result.DeletedCount++
		case SourceStateUnchanged:
			result.UnchangedCount++
		default:
			if isNew {
				result.NewCount++
			}
		}
	}
	return result, nil
}

func (r *DBStateReducer) mutateSeenState(ctx context.Context, input crawl.ReduceSeenInput, object store.SourceObject) (store.DocumentState, bool, error) {
	mutator, ok := r.store.(stateMutationStore)
	if !ok {
		next, isNew, err := r.nextSeenState(ctx, input, object)
		if err != nil {
			return store.DocumentState{}, false, err
		}
		if err := r.store.SaveDocumentState(ctx, next); err != nil {
			return store.DocumentState{}, false, err
		}
		return next, isNew, nil
	}
	var isNew bool
	next, err := mutator.MutateDocumentState(ctx, input.SourceID, input.BindingID, object.ObjectKey, func(current store.DocumentState, create bool) (store.DocumentState, error) {
		isNew = create
		if create {
			return r.newSeenState(input, object), nil
		}
		return r.updateSeenState(input, object, current), nil
	})
	return next, isNew, err
}

func (r *DBStateReducer) nextSeenState(ctx context.Context, input crawl.ReduceSeenInput, object store.SourceObject) (store.DocumentState, bool, error) {
	now := input.DetectedAt
	if now.IsZero() {
		now = r.clock()
	}
	current, err := r.store.GetDocumentState(ctx, input.SourceID, input.BindingID, object.ObjectKey)
	if err != nil {
		if store.ErrorCodeOf(err) != store.ErrCodeNotFound {
			return store.DocumentState{}, false, err
		}
		return store.DocumentState{
			SourceID:            input.SourceID,
			BindingID:           input.BindingID,
			BindingGeneration:   input.BindingGeneration,
			ObjectKey:           object.ObjectKey,
			SourceVersion:       object.SourceVersion,
			DeletedAtSource:     object.DeletedAtSource,
			SourceState:         stateForSeenObject("", object),
			SyncState:           SyncStateIdle,
			PendingAction:       pendingActionForSeenObject("", object),
			DocumentListVisible: true,
			Selectable:          true,
			ParseQueueState:     ParseQueueStateNone,
			LastDetectedAt:      &now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}, true, nil
	}
	current.BindingGeneration = input.BindingGeneration
	current.SourceVersion = object.SourceVersion
	current.DeletedAtSource = object.DeletedAtSource
	current.SourceState = stateForSeenObject(current.BaselineVersion, object)
	current.PendingAction = pendingActionForSeenObject(current.BaselineVersion, object)
	current.DocumentListVisible = true
	current.Selectable = true
	if current.SyncState == "" {
		current.SyncState = SyncStateIdle
	}
	if current.PendingAction == "" {
		current.ParseQueueState = ParseQueueStateNone
	}
	current.LastDetectedAt = &now
	current.UpdatedAt = now
	return current, false, nil
}

func (r *DBStateReducer) newSeenState(input crawl.ReduceSeenInput, object store.SourceObject) store.DocumentState {
	now := input.DetectedAt
	if now.IsZero() {
		now = r.clock()
	}
	return store.DocumentState{
		SourceID:            input.SourceID,
		BindingID:           input.BindingID,
		BindingGeneration:   input.BindingGeneration,
		ObjectKey:           object.ObjectKey,
		SourceVersion:       object.SourceVersion,
		DeletedAtSource:     object.DeletedAtSource,
		SourceState:         stateForSeenObject("", object),
		SyncState:           SyncStateIdle,
		PendingAction:       pendingActionForSeenObject("", object),
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     ParseQueueStateNone,
		LastDetectedAt:      &now,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func (r *DBStateReducer) updateSeenState(input crawl.ReduceSeenInput, object store.SourceObject, current store.DocumentState) store.DocumentState {
	now := input.DetectedAt
	if now.IsZero() {
		now = r.clock()
	}
	current.BindingGeneration = input.BindingGeneration
	current.SourceVersion = object.SourceVersion
	current.DeletedAtSource = object.DeletedAtSource
	current.SourceState = stateForSeenObject(current.BaselineVersion, object)
	current.PendingAction = pendingActionForSeenObject(current.BaselineVersion, object)
	current.DocumentListVisible = true
	current.Selectable = true
	if current.SyncState == "" {
		current.SyncState = SyncStateIdle
	}
	if current.PendingAction == "" {
		current.ParseQueueState = ParseQueueStateNone
	}
	current.LastDetectedAt = &now
	current.UpdatedAt = now
	return current
}

func (r *DBStateReducer) ReduceMissingObjects(ctx context.Context, input crawl.ReduceMissingInput) (crawl.ReduceMissingResult, error) {
	if !input.RunSucceeded || !input.Coverage.Complete {
		return crawl.ReduceMissingResult{}, nil
	}
	states, err := r.store.ListDocumentStates(ctx, input.SourceID, input.BindingID)
	if err != nil {
		return crawl.ReduceMissingResult{}, err
	}
	seen := stringSet(input.SeenObjectKeys)
	now := input.DetectedAt
	if now.IsZero() {
		now = r.clock()
	}
	result := crawl.ReduceMissingResult{}
	for _, current := range states {
		if current.SourceState == SourceStateDeleted || current.ObjectKey == "" {
			continue
		}
		if _, ok := seen[current.ObjectKey]; ok {
			continue
		}
		covered, err := r.coveredByMissingRule(ctx, input, current.ObjectKey)
		if err != nil {
			return crawl.ReduceMissingResult{}, err
		}
		if !covered {
			continue
		}
		next, changed, err := r.mutateMissingState(ctx, input, current, now)
		if err != nil {
			return crawl.ReduceMissingResult{}, err
		}
		if changed && next.SourceState == SourceStateDeleted && next.PendingAction == PendingActionDelete {
			result.DeletedCount++
			result.AffectedObjectKeys = append(result.AffectedObjectKeys, current.ObjectKey)
		}
	}
	slices.Sort(result.AffectedObjectKeys)
	return result, nil
}

func (r *DBStateReducer) mutateMissingState(ctx context.Context, input crawl.ReduceMissingInput, current store.DocumentState, now time.Time) (store.DocumentState, bool, error) {
	mutator, ok := r.store.(stateMutationStore)
	if !ok {
		current.BindingGeneration = input.BindingGeneration
		current.SourceState = SourceStateDeleted
		current.PendingAction = PendingActionDelete
		current.ParseQueueState = ParseQueueStateNone
		current.DocumentListVisible = true
		current.Selectable = true
		current.LastDetectedAt = &now
		current.UpdatedAt = now
		if err := r.store.SaveDocumentState(ctx, current); err != nil {
			return store.DocumentState{}, false, err
		}
		return current, true, nil
	}
	changed := false
	next, err := mutator.MutateDocumentState(ctx, input.SourceID, input.BindingID, current.ObjectKey, func(locked store.DocumentState, create bool) (store.DocumentState, error) {
		if create {
			return locked, store.NewStoreError(store.ErrCodeNotFound, "document state not found")
		}
		if locked.SourceState == SourceStateDeleted {
			return locked, nil
		}
		changed = true
		locked.BindingGeneration = input.BindingGeneration
		locked.SourceState = SourceStateDeleted
		locked.PendingAction = PendingActionDelete
		locked.ParseQueueState = ParseQueueStateNone
		locked.DocumentListVisible = true
		locked.Selectable = true
		locked.LastDetectedAt = &now
		locked.UpdatedAt = now
		return locked, nil
	})
	return next, changed, err
}

func (r *DBStateReducer) ApplyTaskSuccess(ctx context.Context, input TaskSuccessInput) error {
	if input.Task.Status != "SUCCEEDED" {
		return nil
	}
	current, err := r.store.GetDocumentState(ctx, input.Task.SourceID, input.Task.BindingID, input.Task.ObjectKey)
	if err != nil {
		return err
	}
	if current.BindingGeneration != input.Task.BindingGeneration || current.ActiveTaskID != input.Task.TaskID {
		return ErrSuperseded
	}
	if input.Task.TaskAction != PendingActionDelete && current.SourceVersion != input.Task.SourceVersion {
		return ErrSuperseded
	}
	now := input.CompletedAt
	if now.IsZero() {
		now = r.clock()
	}
	if input.Task.TaskAction == PendingActionDelete {
		current.DocumentListVisible = false
		current.Selectable = false
		current.PendingAction = ""
		current.ParseQueueState = ParseQueueStateNone
		current.ActiveTaskID = ""
		current.LastSyncedAt = &now
		current.LastError = store.JSON{}
		current.UpdatedAt = now
		return r.store.SaveDocumentState(ctx, current)
	}
	current.BaselineVersion = input.Task.TargetVersionID
	current.SourceState = SourceStateUnchanged
	current.PendingAction = ""
	current.ParseQueueState = ParseQueueStateNone
	current.ActiveTaskID = ""
	current.LastSyncedAt = &now
	current.LastError = store.JSON{}
	current.UpdatedAt = now
	if err := r.store.SaveDocumentState(ctx, current); err != nil {
		return err
	}
	document, err := r.store.GetDocument(ctx, input.Task.SourceID, input.Task.BindingID, input.Task.ObjectKey)
	if err != nil {
		return err
	}
	document.CoreDocumentID = input.CoreDocumentID
	document.CurrentVersionID = input.CoreVersionID
	document.SourceVersion = input.Task.SourceVersion
	document.ParseStatus = "SUCCEEDED"
	document.UpdatedAt = now
	return r.store.UpdateDocument(ctx, document)
}

func (r *DBStateReducer) ApplyTaskFailure(ctx context.Context, input TaskFailureInput) error {
	current, err := r.store.GetDocumentState(ctx, input.Task.SourceID, input.Task.BindingID, input.Task.ObjectKey)
	if err != nil {
		return err
	}
	if current.ActiveTaskID == input.Task.TaskID {
		current.ParseQueueState = ParseQueueStateFailed
		current.LastError = store.JSON{"code": input.ErrorCode, "message": input.Message}
		now := input.FailedAt
		if now.IsZero() {
			now = r.clock()
		}
		current.UpdatedAt = now
		return r.store.SaveDocumentState(ctx, current)
	}
	return nil
}

func stateForSeenObject(baseline string, object store.SourceObject) string {
	if object.DeletedAtSource != nil {
		return SourceStateDeleted
	}
	if baseline == "" {
		return SourceStateNew
	}
	if object.SourceVersion == baseline {
		return SourceStateUnchanged
	}
	return SourceStateModified
}

func pendingActionForSeenObject(baseline string, object store.SourceObject) string {
	switch stateForSeenObject(baseline, object) {
	case SourceStateNew:
		return PendingActionCreate
	case SourceStateModified:
		return PendingActionReparse
	case SourceStateDeleted:
		return PendingActionDelete
	default:
		return ""
	}
}

func (r *DBStateReducer) coveredByMissingRule(ctx context.Context, input crawl.ReduceMissingInput, objectKey string) (bool, error) {
	switch input.Coverage.ScopeType {
	case connector.ScopeTypeFull:
		return input.Coverage.CoveredTargetRoot, nil
	case connector.ScopeTypePartial:
		if contains(input.Coverage.CoveredObjectKeys, objectKey) || contains(input.Coverage.CoveredSubtrees, objectKey) {
			return true, nil
		}
		return r.objectIsInCoveredSubtree(ctx, input.SourceID, input.BindingID, objectKey, input.Coverage.CoveredSubtrees)
	case connector.ScopeTypeDelta, connector.ScopeTypeWatchEvent:
		return contains(input.Coverage.CoveredObjectKeys, objectKey), nil
	default:
		return false, nil
	}
}

func (r *DBStateReducer) objectIsInCoveredSubtree(ctx context.Context, sourceID, bindingID, objectKey string, subtrees []string) (bool, error) {
	if len(subtrees) == 0 {
		return false, nil
	}
	visited := map[string]struct{}{}
	for key := objectKey; key != ""; {
		if _, ok := visited[key]; ok {
			return false, nil
		}
		visited[key] = struct{}{}
		object, err := r.store.GetObject(ctx, sourceID, bindingID, key)
		if err != nil {
			if store.ErrorCodeOf(err) == store.ErrCodeNotFound {
				return false, nil
			}
			return false, err
		}
		if contains(subtrees, object.ObjectKey) {
			return true, nil
		}
		key = object.ParentKey
	}
	return false, nil
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
