package state

import (
	"context"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/crawl"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestReduceMissingPartialSubtreeMarksOnlyDescendantsDeleted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.objects["folder"] = sourceObject("folder", "", false, true)
	repo.objects["folder/doc-missing"] = sourceObject("folder/doc-missing", "folder", true, false)
	repo.objects["other/doc"] = sourceObject("other/doc", "", true, false)
	repo.states["folder/doc-missing"] = documentState("folder/doc-missing", now)
	repo.states["other/doc"] = documentState("other/doc", now)
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	result, err := reducer.ReduceMissingObjects(ctx, crawl.ReduceMissingInput{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 2,
		RunID:             "run-1",
		Coverage: crawl.Coverage{
			ScopeType:       connector.ScopeTypePartial,
			CoveredSubtrees: []string{"folder"},
			Complete:        true,
		},
		SeenObjectKeys: []string{"folder"},
		RunSucceeded:   true,
		DetectedAt:     now,
	})
	if err != nil {
		t.Fatalf("reduce missing: %v", err)
	}
	if result.DeletedCount != 1 || len(result.AffectedObjectKeys) != 1 || result.AffectedObjectKeys[0] != "folder/doc-missing" {
		t.Fatalf("expected only subtree descendant to be deleted, got %+v", result)
	}
	if got := repo.states["folder/doc-missing"]; got.SourceState != SourceStateDeleted || got.PendingAction != PendingActionDelete {
		t.Fatalf("missing subtree descendant was not marked deleted: %+v", got)
	}
	if got := repo.states["other/doc"]; got.SourceState != SourceStateUnchanged || got.PendingAction != "" {
		t.Fatalf("coverage outside subtree was modified: %+v", got)
	}
}

func TestReduceMissingIgnoresIncompleteCoverage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.objects["doc"] = sourceObject("doc", "", true, false)
	repo.states["doc"] = documentState("doc", now)
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	result, err := reducer.ReduceMissingObjects(ctx, crawl.ReduceMissingInput{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		Coverage: crawl.Coverage{
			ScopeType:         connector.ScopeTypeFull,
			CoveredTargetRoot: true,
			Complete:          false,
		},
		RunSucceeded: true,
		DetectedAt:   now,
	})
	if err != nil {
		t.Fatalf("reduce missing: %v", err)
	}
	if result.DeletedCount != 0 {
		t.Fatalf("incomplete coverage must not delete, got %+v", result)
	}
	if got := repo.states["doc"]; got.SourceState != SourceStateUnchanged {
		t.Fatalf("incomplete coverage changed state: %+v", got)
	}
}

func TestReduceMissingDeltaAndWatchOnlyDeleteCoveredKeys(t *testing.T) {
	t.Parallel()

	for _, scopeType := range []connector.ScopeType{connector.ScopeTypeDelta, connector.ScopeTypeWatchEvent} {
		t.Run(string(scopeType), func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
			repo := newReducerStore()
			repo.objects["covered"] = sourceObject("covered", "", true, false)
			repo.objects["outside"] = sourceObject("outside", "", true, false)
			repo.states["covered"] = documentState("covered", now)
			repo.states["outside"] = documentState("outside", now)
			reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

			result, err := reducer.ReduceMissingObjects(ctx, crawl.ReduceMissingInput{
				SourceID:          "source-1",
				BindingID:         "binding-1",
				BindingGeneration: 2,
				RunID:             "run-1",
				Coverage: crawl.Coverage{
					ScopeType:         scopeType,
					CoveredObjectKeys: []string{"covered"},
					Complete:          true,
				},
				RunSucceeded: true,
				DetectedAt:   now,
			})
			if err != nil {
				t.Fatalf("reduce missing: %v", err)
			}
			if result.DeletedCount != 1 || result.AffectedObjectKeys[0] != "covered" {
				t.Fatalf("expected only covered key to delete, got %+v", result)
			}
			if got := repo.states["outside"]; got.SourceState != SourceStateUnchanged || got.PendingAction != "" {
				t.Fatalf("outside key was changed: %+v", got)
			}
		})
	}
}

func TestReduceSeenUnsupportedDocumentIsHiddenAndNotCounted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.binding.IncludeExtensions = store.JSON{"items": []any{"pdf"}}
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	result, err := reducer.ReduceSeenObjects(ctx, crawl.ReduceSeenInput{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		RunID:             "run-1",
		Objects: []store.SourceObject{{
			SourceID:      "source-1",
			BindingID:     "binding-1",
			ObjectKey:     "script",
			DisplayName:   "script.py",
			IsDocument:    true,
			FileExtension: ".py",
			SourceVersion: "v1",
		}},
		DetectedAt: now,
	})
	if err != nil {
		t.Fatalf("reduce seen: %v", err)
	}
	if result.NewCount != 0 {
		t.Fatalf("unsupported document should not count as new: %+v", result)
	}
	got := repo.states["script"]
	if got.DocumentListVisible || got.Selectable || got.PendingAction != "" || got.SourceState != SourceStateUnchanged {
		t.Fatalf("unsupported document should be hidden and idle: %+v", got)
	}
}

func TestReduceSeenSyncedUnsupportedDocumentIsMarkedOutOfScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.binding.IncludeExtensions = store.JSON{"items": []any{"pdf"}}
	repo.states["script"] = documentState("script", now)
	synced := repo.states["script"]
	synced.BaselineVersion = "v1"
	synced.DocumentID = "document-script"
	repo.states["script"] = synced
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	result, err := reducer.ReduceSeenObjects(ctx, crawl.ReduceSeenInput{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		RunID:             "run-1",
		Objects: []store.SourceObject{{
			SourceID:      "source-1",
			BindingID:     "binding-1",
			ObjectKey:     "script",
			DisplayName:   "script.py",
			IsDocument:    true,
			FileExtension: ".py",
			SourceVersion: "v1",
		}},
		DetectedAt: now,
	})
	if err != nil {
		t.Fatalf("reduce seen: %v", err)
	}
	if result.NewCount != 0 || result.ModifiedCount != 0 || result.DeletedCount != 0 {
		t.Fatalf("out-of-scope document should not count as source create/update/delete: %+v", result)
	}
	got := repo.states["script"]
	if got.SourceState != SourceStateOutOfScope || got.PendingAction != PendingActionDelete {
		t.Fatalf("synced unsupported document should be marked pending cleanup: %+v", got)
	}
	if !got.DocumentListVisible || !got.Selectable {
		t.Fatalf("pending cleanup document should remain visible/selectable: %+v", got)
	}
}

func TestReduceSeenUsesSourceIncludeExtensionsWhenBindingIncludeIsEmpty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.source.IncludeExtensions = store.JSON{"items": []any{"pdf"}}
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	result, err := reducer.ReduceSeenObjects(ctx, crawl.ReduceSeenInput{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		RunID:             "run-1",
		Objects: []store.SourceObject{{
			SourceID:      "source-1",
			BindingID:     "binding-1",
			ObjectKey:     "script",
			DisplayName:   "script.py",
			IsDocument:    true,
			FileExtension: ".py",
			SourceVersion: "v1",
		}},
		DetectedAt: now,
	})
	if err != nil {
		t.Fatalf("reduce seen: %v", err)
	}
	if result.NewCount != 0 {
		t.Fatalf("unsupported document should not count as new: %+v", result)
	}
	got := repo.states["script"]
	if got.DocumentListVisible || got.Selectable {
		t.Fatalf("source include extensions should hide unsupported documents: %+v", got)
	}
}

func TestReduceSeenUnsupportedContainerRemainsVisibleForNavigation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.binding.IncludeExtensions = store.JSON{"items": []any{"pdf"}}
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	_, err := reducer.ReduceSeenObjects(ctx, crawl.ReduceSeenInput{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		RunID:             "run-1",
		Objects: []store.SourceObject{{
			SourceID:      "source-1",
			BindingID:     "binding-1",
			ObjectKey:     "wiki-page",
			DisplayName:   "Wiki Page",
			IsDocument:    true,
			IsContainer:   true,
			HasChildren:   true,
			FileExtension: ".py",
			SourceVersion: "v1",
		}},
		DetectedAt: now,
	})
	if err != nil {
		t.Fatalf("reduce seen: %v", err)
	}
	got := repo.states["wiki-page"]
	if !got.DocumentListVisible || got.Selectable || got.PendingAction != "" {
		t.Fatalf("unsupported document container should stay visible but not selectable: %+v", got)
	}
}

func TestApplyDeleteTaskSuccessClearsSyncedBaseline(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.states["script"] = documentState("script", now)
	state := repo.states["script"]
	state.SourceState = SourceStateOutOfScope
	state.PendingAction = PendingActionDelete
	state.ActiveTaskID = "task-1"
	state.DocumentID = "document-script"
	repo.states["script"] = state
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	err := reducer.ApplyTaskSuccess(ctx, TaskSuccessInput{
		Task: store.ParseTask{
			TaskID:            "task-1",
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			ObjectKey:         "script",
			TaskAction:        PendingActionDelete,
			TargetVersionID:   "v1",
			Status:            "SUCCEEDED",
		},
		CompletedAt: now,
	})
	if err != nil {
		t.Fatalf("apply delete success: %v", err)
	}
	got := repo.states["script"]
	if got.BaselineVersion != "" || got.DocumentID != "" {
		t.Fatalf("delete success should clear synced baseline and document link: %+v", got)
	}
	if got.DocumentListVisible || got.Selectable || got.PendingAction != "" {
		t.Fatalf("delete success should hide and clear pending cleanup: %+v", got)
	}
}

func TestApplyTaskFailureMarksDocumentFailed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.states["doc"] = documentState("doc", now)
	state := repo.states["doc"]
	state.ActiveTaskID = "task-1"
	state.ParseQueueState = ParseQueueStateQueued
	repo.states["doc"] = state
	repo.documents["doc"] = store.Document{
		DocumentID:  "document-1",
		SourceID:    "source-1",
		BindingID:   "binding-1",
		ObjectKey:   "doc",
		ParseStatus: "SUCCEEDED",
		UpdatedAt:   now,
	}
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	err := reducer.ApplyTaskFailure(ctx, TaskFailureInput{
		Task: store.ParseTask{
			TaskID:            "task-1",
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			ObjectKey:         "doc",
		},
		ErrorCode: "PARSE_FAILED",
		Message:   "bad file",
		Phase:     "parse",
		FailedAt:  now,
	})
	if err != nil {
		t.Fatalf("apply task failure: %v", err)
	}
	if got := repo.states["doc"]; got.ParseQueueState != ParseQueueStateFailed || got.LastError["code"] != "PARSE_FAILED" || got.LastError["phase"] != "parse" {
		t.Fatalf("document state did not record failure: %+v", got)
	}
	if got := repo.documents["doc"]; got.ParseStatus != "FAILED" {
		t.Fatalf("document parse status should be failed, got %+v", got)
	}
}

func TestApplyTaskFailurePreservesCanceledDocumentStatus(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.states["doc"] = documentState("doc", now)
	state := repo.states["doc"]
	state.ActiveTaskID = "task-1"
	state.ParseQueueState = ParseQueueStateQueued
	repo.states["doc"] = state
	repo.documents["doc"] = store.Document{
		DocumentID:  "document-1",
		SourceID:    "source-1",
		BindingID:   "binding-1",
		ObjectKey:   "doc",
		ParseStatus: "PENDING",
		UpdatedAt:   now,
	}
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	err := reducer.ApplyTaskFailure(ctx, TaskFailureInput{
		Task: store.ParseTask{
			TaskID:            "task-1",
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			ObjectKey:         "doc",
		},
		ErrorCode: "CANCELED",
		Message:   "canceled in knowledge base",
		Phase:     "parse",
		FailedAt:  now,
	})
	if err != nil {
		t.Fatalf("apply task failure: %v", err)
	}
	if got := repo.documents["doc"]; got.ParseStatus != "CANCELED" {
		t.Fatalf("document parse status should be canceled, got %+v", got)
	}
}

func TestApplyTaskFailureIgnoresReplacedTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newReducerStore()
	repo.states["doc"] = documentState("doc", now)
	state := repo.states["doc"]
	state.ActiveTaskID = "task-current"
	state.ParseQueueState = ParseQueueStateQueued
	repo.states["doc"] = state
	repo.documents["doc"] = store.Document{
		DocumentID:  "document-1",
		SourceID:    "source-1",
		BindingID:   "binding-1",
		ObjectKey:   "doc",
		ParseStatus: "PENDING",
		UpdatedAt:   now,
	}
	reducer := NewDBStateReducer(repo, WithClock(func() time.Time { return now }))

	err := reducer.ApplyTaskFailure(ctx, TaskFailureInput{
		Task: store.ParseTask{
			TaskID:            "task-old",
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			ObjectKey:         "doc",
		},
		ErrorCode: "PARSE_FAILED",
		Message:   "old task failed",
		FailedAt:  now,
	})
	if err != nil {
		t.Fatalf("apply task failure: %v", err)
	}
	if got := repo.states["doc"]; got.ParseQueueState != ParseQueueStateQueued || len(got.LastError) != 0 {
		t.Fatalf("replaced task should not change state: %+v", got)
	}
	if got := repo.documents["doc"]; got.ParseStatus != "PENDING" {
		t.Fatalf("replaced task should not change document status: %+v", got)
	}
}

type reducerStore struct {
	objects   map[string]store.SourceObject
	states    map[string]store.DocumentState
	documents map[string]store.Document
	source    store.Source
	binding   store.Binding
}

func newReducerStore() *reducerStore {
	return &reducerStore{
		objects:   map[string]store.SourceObject{},
		states:    map[string]store.DocumentState{},
		documents: map[string]store.Document{},
		source: store.Source{
			SourceID: "source-1",
		},
		binding: store.Binding{
			SourceID:  "source-1",
			BindingID: "binding-1",
		},
	}
}

func (s *reducerStore) GetSource(context.Context, string) (store.Source, error) {
	return s.source, nil
}

func (s *reducerStore) GetBinding(context.Context, string, string) (store.Binding, error) {
	return s.binding, nil
}

func (s *reducerStore) GetDocumentState(_ context.Context, _, _, objectKey string) (store.DocumentState, error) {
	state, ok := s.states[objectKey]
	if !ok {
		return store.DocumentState{}, store.NewStoreError(store.ErrCodeNotFound, "state not found")
	}
	return state, nil
}

func (s *reducerStore) SaveDocumentState(_ context.Context, state store.DocumentState) error {
	s.states[state.ObjectKey] = state
	return nil
}

func (s *reducerStore) MutateDocumentState(_ context.Context, _, _, objectKey string, mutate store.DocumentStateMutation) (store.DocumentState, error) {
	current, exists := s.states[objectKey]
	next, err := mutate(current, !exists)
	if err != nil {
		return store.DocumentState{}, err
	}
	s.states[next.ObjectKey] = next
	return next, nil
}

func (s *reducerStore) ListDocumentStates(context.Context, string, string) ([]store.DocumentState, error) {
	states := make([]store.DocumentState, 0, len(s.states))
	for _, state := range s.states {
		states = append(states, state)
	}
	return states, nil
}

func (s *reducerStore) GetObject(_ context.Context, _, _, objectKey string) (store.SourceObject, error) {
	object, ok := s.objects[objectKey]
	if !ok {
		return store.SourceObject{}, store.NewStoreError(store.ErrCodeNotFound, "object not found")
	}
	return object, nil
}

func (s *reducerStore) UpdateDocument(_ context.Context, document store.Document) error {
	s.documents[document.ObjectKey] = document
	return nil
}

func (s *reducerStore) GetDocument(_ context.Context, _, _, objectKey string) (store.Document, error) {
	document, ok := s.documents[objectKey]
	if !ok {
		return store.Document{}, store.NewStoreError(store.ErrCodeNotFound, "document not found")
	}
	return document, nil
}

func sourceObject(objectKey, parentKey string, isDocument, isContainer bool) store.SourceObject {
	return store.SourceObject{
		SourceID:    "source-1",
		BindingID:   "binding-1",
		ObjectKey:   objectKey,
		ParentKey:   parentKey,
		IsDocument:  isDocument,
		IsContainer: isContainer,
	}
}

func documentState(objectKey string, now time.Time) store.DocumentState {
	return store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           objectKey,
		SourceVersion:       "v1",
		BaselineVersion:     "v1",
		SourceState:         SourceStateUnchanged,
		SyncState:           SyncStateIdle,
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     ParseQueueStateNone,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

var _ Store = (*reducerStore)(nil)
