package tree

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestSourceReadRefresherMarksFeishuMissingDocumentDeleted(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 8, 30, 0, 0, time.UTC)
	repo := newRefreshTestRepo(now)
	repo.objects["doc-missing"] = refreshObject("doc-missing", "root", true, false, now)
	repo.states["doc-missing"] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           "doc-missing",
		SourceVersion:       "old-v1",
		BaselineVersion:     "old-v1",
		SourceState:         "UNCHANGED",
		SyncState:           "IDLE",
		DocumentListVisible: true,
		Selectable:          true,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	conn := &treeConnectorSpy{
		connectorType: connector.ConnectorType("feishu"),
		childrenSet:   true,
		children:      []connector.RawObject{rawTreeObject("doc-current", "", "Current.md", true, false)},
	}
	registry, err := connector.NewDefaultConnectorRegistry(conn)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	refresher := NewDBSourceReadRefresher(repo, registry, WithSourceReadRefreshClock(func() time.Time { return now }))

	if err := refresher.RefreshSourceRead(context.Background(), SourceReadRefreshRequest{SourceID: "source-1", BindingID: "binding-1"}); err != nil {
		t.Fatalf("refresh source read: %v", err)
	}

	missing := repo.states["doc-missing"]
	if missing.SourceState != "DELETED" || missing.PendingAction != "DELETE" || !missing.DocumentListVisible || !missing.Selectable {
		t.Fatalf("missing source document should be pending delete and visible: %+v", missing)
	}
	current := repo.states["doc-current"]
	if current.SourceState != "NEW" || current.PendingAction != "CREATE" || !current.DocumentListVisible {
		t.Fatalf("seen source document should still be reduced: %+v", current)
	}
}

func TestSourceReadRefresherReportsMissingBindingTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "not found", err: connector.NewError(connector.ErrorCodeNotFound, "wiki node not found")},
		{name: "transient not found", err: connector.NewError(connector.ErrorCodeTransient, "not found")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 6, 16, 8, 30, 0, 0, time.UTC)
			repo := newRefreshTestRepo(now)
			repo.binding.TargetType = "wiki_node"
			repo.binding.TargetRef = "wiki:space-1:node-root"
			repo.binding.CoreParentDocumentName = "Root Wiki"
			repo.states["doc-existing"] = store.DocumentState{
				SourceID:            "source-1",
				BindingID:           "binding-1",
				BindingGeneration:   1,
				ObjectKey:           "doc-existing",
				SourceVersion:       "old-v1",
				BaselineVersion:     "old-v1",
				SourceState:         "UNCHANGED",
				SyncState:           "IDLE",
				DocumentListVisible: true,
				Selectable:          true,
				CreatedAt:           now,
				UpdatedAt:           now,
			}
			conn := &treeConnectorSpy{
				connectorType: connector.ConnectorType("feishu"),
				listErr:       tt.err,
			}
			registry, err := connector.NewDefaultConnectorRegistry(conn)
			if err != nil {
				t.Fatalf("create registry: %v", err)
			}
			refresher := NewDBSourceReadRefresher(repo, registry, WithSourceReadRefreshClock(func() time.Time { return now }))

			err = refresher.RefreshSourceRead(context.Background(), SourceReadRefreshRequest{SourceID: "source-1", BindingID: "binding-1"})
			if err == nil {
				t.Fatalf("expected missing target error")
			}
			if got := ErrorCodeOf(err); got != ErrCodeTargetNotFound {
				t.Fatalf("expected error code %s, got %s (%v)", ErrCodeTargetNotFound, got, err)
			}
			if msg := err.Error(); !strings.Contains(msg, "Root Wiki") || !strings.Contains(msg, "wiki:space-1:node-root") {
				t.Fatalf("expected target name and ref in error message, got %q", msg)
			}
			existing := repo.states["doc-existing"]
			if existing.SourceState == "DELETED" || existing.PendingAction == "DELETE" {
				t.Fatalf("binding target missing should not mark existing states deleted: %+v", existing)
			}
		})
	}
}

func TestSourceReadRefresherDoesNotFetchNonFeishuBindings(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 8, 30, 0, 0, time.UTC)
	repo := newRefreshTestRepo(now)
	repo.binding.ConnectorType = "local_fs"
	conn := &treeConnectorSpy{}
	registry, err := connector.NewDefaultConnectorRegistry(conn)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	refresher := NewDBSourceReadRefresher(repo, registry, WithSourceReadRefreshClock(func() time.Time { return now }))

	if err := refresher.RefreshSourceRead(context.Background(), SourceReadRefreshRequest{SourceID: "source-1", BindingID: "binding-1"}); err != nil {
		t.Fatalf("refresh source read: %v", err)
	}
	if len(conn.listRequests) != 0 || len(repo.objects) != 0 || len(repo.states) != 0 {
		t.Fatalf("non-feishu read should not refresh connector or state, lists=%d objects=%+v states=%+v", len(conn.listRequests), repo.objects, repo.states)
	}
}

func TestSourceReadRefresherRefreshesCachedNonFeishuPolicyState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 8, 30, 0, 0, time.UTC)
	repo := newRefreshTestRepo(now)
	repo.binding.ConnectorType = "local_fs"
	repo.binding.IncludeExtensions = store.JSON{"items": []any{"xlsx"}}

	syncedPDF := refreshObject("synced.pdf", "root", true, false, now)
	syncedPDF.FileExtension = ".pdf"
	repo.objects[syncedPDF.ObjectKey] = syncedPDF
	repo.states[syncedPDF.ObjectKey] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           syncedPDF.ObjectKey,
		SourceVersion:       syncedPDF.SourceVersion,
		SourceState:         "UNCHANGED",
		SyncState:           "IDLE",
		DocumentID:          "document-synced",
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     "NONE",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	repo.tasks = append(repo.tasks, store.ParseTask{
		TaskID:            "task-synced-create",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ObjectKey:         syncedPDF.ObjectKey,
		DocumentID:        "document-synced",
		TaskAction:        store.ParseTaskActionCreate,
		TargetVersionID:   syncedPDF.SourceVersion,
		SourceVersion:     syncedPDF.SourceVersion,
		Status:            store.ParseTaskStatusSucceeded,
		CreatedAt:         now.Add(-time.Minute),
		UpdatedAt:         now.Add(-time.Minute),
	})

	syncedWithoutStateDocID := refreshObject("synced-missing-state-doc-id.pdf", "root", true, false, now)
	syncedWithoutStateDocID.FileExtension = ".pdf"
	repo.objects[syncedWithoutStateDocID.ObjectKey] = syncedWithoutStateDocID
	repo.states[syncedWithoutStateDocID.ObjectKey] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           syncedWithoutStateDocID.ObjectKey,
		SourceVersion:       syncedWithoutStateDocID.SourceVersion,
		SourceState:         "NEW",
		SyncState:           "IDLE",
		PendingAction:       "CREATE",
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     "NONE",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	repo.tasks = append(repo.tasks, store.ParseTask{
		TaskID:            "task-synced-missing-state-doc-id-create",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ObjectKey:         syncedWithoutStateDocID.ObjectKey,
		DocumentID:        "document-synced-missing-state-doc-id",
		TaskAction:        store.ParseTaskActionCreate,
		TargetVersionID:   syncedWithoutStateDocID.SourceVersion,
		SourceVersion:     syncedWithoutStateDocID.SourceVersion,
		Status:            store.ParseTaskStatusSucceeded,
		CreatedAt:         now.Add(-time.Minute),
		UpdatedAt:         now.Add(-time.Minute),
	})

	newPDF := refreshObject("new.pdf", "root", true, false, now)
	newPDF.FileExtension = ".pdf"
	repo.objects[newPDF.ObjectKey] = newPDF
	repo.states[newPDF.ObjectKey] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           newPDF.ObjectKey,
		SourceVersion:       newPDF.SourceVersion,
		DocumentID:          "document-new",
		SourceState:         "NEW",
		SyncState:           "IDLE",
		PendingAction:       "CREATE",
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     "QUEUED",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	repo.tasks = append(repo.tasks, store.ParseTask{
		TaskID:            "task-new-create",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ObjectKey:         newPDF.ObjectKey,
		DocumentID:        "document-new",
		TaskAction:        store.ParseTaskActionCreate,
		TargetVersionID:   newPDF.SourceVersion,
		SourceVersion:     newPDF.SourceVersion,
		Status:            store.ParseTaskStatusPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	})

	documentOnlyPDF := refreshObject("document-only.pdf", "root", true, false, now)
	documentOnlyPDF.FileExtension = ".pdf"
	repo.objects[documentOnlyPDF.ObjectKey] = documentOnlyPDF
	repo.states[documentOnlyPDF.ObjectKey] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           documentOnlyPDF.ObjectKey,
		SourceVersion:       documentOnlyPDF.SourceVersion,
		SourceState:         "NEW",
		SyncState:           "IDLE",
		PendingAction:       "CREATE",
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     "NONE",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	repo.documents[documentOnlyPDF.ObjectKey] = store.Document{
		DocumentID:     "document-only-synced",
		SourceID:       "source-1",
		BindingID:      "binding-1",
		ObjectKey:      documentOnlyPDF.ObjectKey,
		CoreDocumentID: "core-document-only-synced",
		SourceVersion:  documentOnlyPDF.SourceVersion,
		ParseStatus:    "SUCCEEDED",
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
	}

	cleanedPDF := refreshObject("cleaned.pdf", "root", true, false, now)
	cleanedPDF.FileExtension = ".pdf"
	repo.objects[cleanedPDF.ObjectKey] = cleanedPDF
	repo.states[cleanedPDF.ObjectKey] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           cleanedPDF.ObjectKey,
		SourceVersion:       cleanedPDF.SourceVersion,
		DocumentID:          "document-cleaned",
		SourceState:         "UNCHANGED",
		SyncState:           "IDLE",
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     "NONE",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	repo.tasks = append(repo.tasks,
		store.ParseTask{
			TaskID:            "task-cleaned-create",
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			ObjectKey:         cleanedPDF.ObjectKey,
			DocumentID:        "document-cleaned",
			TaskAction:        store.ParseTaskActionCreate,
			TargetVersionID:   cleanedPDF.SourceVersion,
			SourceVersion:     cleanedPDF.SourceVersion,
			Status:            store.ParseTaskStatusSucceeded,
			CreatedAt:         now.Add(-2 * time.Minute),
			UpdatedAt:         now.Add(-2 * time.Minute),
		},
		store.ParseTask{
			TaskID:            "task-cleaned-delete",
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			ObjectKey:         cleanedPDF.ObjectKey,
			DocumentID:        "document-cleaned",
			TaskAction:        store.ParseTaskActionDelete,
			TargetVersionID:   cleanedPDF.SourceVersion,
			SourceVersion:     cleanedPDF.SourceVersion,
			Status:            store.ParseTaskStatusSucceeded,
			CreatedAt:         now.Add(-time.Minute),
			UpdatedAt:         now.Add(-time.Minute),
		},
	)
	repo.documents[cleanedPDF.ObjectKey] = store.Document{
		DocumentID:     "document-cleaned",
		SourceID:       "source-1",
		BindingID:      "binding-1",
		ObjectKey:      cleanedPDF.ObjectKey,
		CoreDocumentID: "core-document-cleaned",
		SourceVersion:  cleanedPDF.SourceVersion,
		ParseStatus:    "SUCCEEDED",
		CreatedAt:      now.Add(-2 * time.Minute),
		UpdatedAt:      now.Add(-2 * time.Minute),
	}

	sheet := refreshObject("sheet.xlsx", "root", true, false, now)
	sheet.FileExtension = ".xlsx"
	repo.objects[sheet.ObjectKey] = sheet
	repo.states[sheet.ObjectKey] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           sheet.ObjectKey,
		SourceVersion:       sheet.SourceVersion,
		SourceState:         "NEW",
		SyncState:           "IDLE",
		PendingAction:       "CREATE",
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     "QUEUED",
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	conn := &treeConnectorSpy{}
	registry, err := connector.NewDefaultConnectorRegistry(conn)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	refresher := NewDBSourceReadRefresher(repo, registry, WithSourceReadRefreshClock(func() time.Time { return now }))

	if err := refresher.RefreshSourceRead(context.Background(), SourceReadRefreshRequest{SourceID: "source-1", BindingID: "binding-1"}); err != nil {
		t.Fatalf("refresh source read: %v", err)
	}
	if len(conn.listRequests) != 0 {
		t.Fatalf("non-feishu read refresh should not fetch connector, lists=%d", len(conn.listRequests))
	}

	gotSyncedPDF := repo.states[syncedPDF.ObjectKey]
	if gotSyncedPDF.SourceState != "OUT_OF_SCOPE" || gotSyncedPDF.PendingAction != "DELETE" || !gotSyncedPDF.DocumentListVisible || !gotSyncedPDF.Selectable {
		t.Fatalf("synced unsupported document should be pending cleanup and visible: %+v", gotSyncedPDF)
	}
	gotSyncedWithoutStateDocID := repo.states[syncedWithoutStateDocID.ObjectKey]
	if gotSyncedWithoutStateDocID.SourceState != "OUT_OF_SCOPE" || gotSyncedWithoutStateDocID.PendingAction != "DELETE" || gotSyncedWithoutStateDocID.DocumentID != "document-synced-missing-state-doc-id" {
		t.Fatalf("historically synced unsupported document without state document_id should be pending cleanup: %+v", gotSyncedWithoutStateDocID)
	}
	gotNewPDF := repo.states[newPDF.ObjectKey]
	if gotNewPDF.SourceState != "UNCHANGED" || gotNewPDF.PendingAction != "" || gotNewPDF.DocumentListVisible || gotNewPDF.Selectable {
		t.Fatalf("unsynced unsupported document should be hidden, got %+v", gotNewPDF)
	}
	gotDocumentOnlyPDF := repo.states[documentOnlyPDF.ObjectKey]
	if gotDocumentOnlyPDF.SourceState != "OUT_OF_SCOPE" || gotDocumentOnlyPDF.PendingAction != "DELETE" || gotDocumentOnlyPDF.DocumentID != "document-only-synced" {
		t.Fatalf("document-backed unsupported document should be pending cleanup: %+v", gotDocumentOnlyPDF)
	}
	gotCleanedPDF := repo.states[cleanedPDF.ObjectKey]
	if gotCleanedPDF.SourceState != "UNCHANGED" || gotCleanedPDF.PendingAction != "" || gotCleanedPDF.DocumentListVisible || gotCleanedPDF.Selectable {
		t.Fatalf("cleaned unsupported document should stay hidden, got %+v", gotCleanedPDF)
	}
	gotSheet := repo.states[sheet.ObjectKey]
	if gotSheet.SourceState != "NEW" || gotSheet.PendingAction != "CREATE" || !gotSheet.DocumentListVisible || !gotSheet.Selectable {
		t.Fatalf("supported document should remain visible and actionable: %+v", gotSheet)
	}
}

type refreshTestRepo struct {
	source    store.Source
	binding   store.Binding
	objects   map[string]store.SourceObject
	states    map[string]store.DocumentState
	documents map[string]store.Document
	tasks     []store.ParseTask
}

func newRefreshTestRepo(now time.Time) *refreshTestRepo {
	return &refreshTestRepo{
		source: store.Source{
			SourceID:  "source-1",
			CreatedBy: "user-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
		binding: store.Binding{
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			ConnectorType:     "feishu",
			TargetType:        string(treeTestTargetType),
			TargetRef:         "tree-test://root",
			TreeKey:           "root",
			Status:            "ACTIVE",
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		objects:   map[string]store.SourceObject{},
		states:    map[string]store.DocumentState{},
		documents: map[string]store.Document{},
		tasks:     []store.ParseTask{},
	}
}

func (r *refreshTestRepo) GetSource(_ context.Context, sourceID string) (store.Source, error) {
	if r.source.SourceID != sourceID {
		return store.Source{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return r.source, nil
}

func (r *refreshTestRepo) ListBindings(_ context.Context, sourceID string) ([]store.Binding, error) {
	if r.source.SourceID != sourceID {
		return nil, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return []store.Binding{r.binding}, nil
}

func (r *refreshTestRepo) GetBinding(_ context.Context, sourceID, bindingID string) (store.Binding, error) {
	if r.binding.SourceID != sourceID || r.binding.BindingID != bindingID {
		return store.Binding{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
	}
	return r.binding, nil
}

func (r *refreshTestRepo) UpsertObjects(_ context.Context, objects []store.SourceObject) error {
	for _, object := range objects {
		r.objects[object.ObjectKey] = object
	}
	return nil
}

func (r *refreshTestRepo) GetDocumentState(_ context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error) {
	state, ok := r.states[objectKey]
	if !ok || state.SourceID != sourceID || state.BindingID != bindingID {
		return store.DocumentState{}, store.NewStoreError(store.ErrCodeNotFound, "document state not found")
	}
	return state, nil
}

func (r *refreshTestRepo) SaveDocumentState(_ context.Context, state store.DocumentState) error {
	r.states[state.ObjectKey] = state
	return nil
}

func (r *refreshTestRepo) ListDocumentStates(_ context.Context, sourceID, bindingID string) ([]store.DocumentState, error) {
	states := make([]store.DocumentState, 0, len(r.states))
	for _, state := range r.states {
		if state.SourceID == sourceID && state.BindingID == bindingID {
			states = append(states, state)
		}
	}
	return states, nil
}

func (r *refreshTestRepo) GetObject(_ context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error) {
	object, ok := r.objects[objectKey]
	if !ok || object.SourceID != sourceID || object.BindingID != bindingID {
		return store.SourceObject{}, store.NewStoreError(store.ErrCodeNotFound, "object not found")
	}
	return object, nil
}

func (r *refreshTestRepo) GetDocument(_ context.Context, sourceID, bindingID, objectKey string) (store.Document, error) {
	document, ok := r.documents[objectKey]
	if !ok || document.SourceID != sourceID || document.BindingID != bindingID {
		return store.Document{}, store.NewStoreError(store.ErrCodeNotFound, "document not found")
	}
	return document, nil
}

func (r *refreshTestRepo) UpdateDocument(_ context.Context, document store.Document) error {
	r.documents[document.ObjectKey] = document
	return nil
}

func (r *refreshTestRepo) ListObjects(context.Context, store.ObjectListRequest) ([]store.ObjectWithState, string, bool, error) {
	return nil, "", false, nil
}

func (r *refreshTestRepo) SearchObjects(context.Context, store.ObjectSearchRequest) ([]store.ObjectWithState, string, bool, error) {
	return nil, "", false, nil
}

func (r *refreshTestRepo) ListDocuments(context.Context, store.SourceDocumentListRequest) ([]store.DocumentWithState, int, error) {
	return nil, 0, nil
}

func (r *refreshTestRepo) GetSourceSummary(context.Context, store.SourceSummaryRequest) (store.SourceSummary, error) {
	return store.SourceSummary{}, nil
}

func (r *refreshTestRepo) ListParseTasks(_ context.Context, req store.ParseTaskListRequest) ([]store.ParseTaskWithRefs, int, error) {
	items := []store.ParseTaskWithRefs{}
	for _, task := range r.tasks {
		if req.SourceID != "" && task.SourceID != req.SourceID {
			continue
		}
		if req.BindingID != "" && task.BindingID != req.BindingID {
			continue
		}
		if len(req.Statuses) > 0 && !containsString(req.Statuses, task.Status) {
			continue
		}
		if len(req.TaskActions) > 0 && !containsString(req.TaskActions, task.TaskAction) {
			continue
		}
		items = append(items, store.ParseTaskWithRefs{Task: task})
	}
	return items, len(items), nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func refreshObject(objectKey, parentKey string, isDocument, isContainer bool, now time.Time) store.SourceObject {
	return store.SourceObject{
		SourceID:      "source-1",
		BindingID:     "binding-1",
		TreeKey:       "root",
		ObjectKey:     objectKey,
		ParentKey:     parentKey,
		DisplayName:   objectKey,
		SearchName:    objectKey,
		ObjectType:    "file",
		IsDocument:    isDocument,
		IsContainer:   isContainer,
		HasChildren:   isContainer,
		SourceVersion: "old-v1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

var _ SourceReadRefreshRepository = (*refreshTestRepo)(nil)
