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

func TestSourceReadRefresherSkipsNonFeishuBindings(t *testing.T) {
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

type refreshTestRepo struct {
	source    store.Source
	binding   store.Binding
	objects   map[string]store.SourceObject
	states    map[string]store.DocumentState
	documents map[string]store.Document
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
