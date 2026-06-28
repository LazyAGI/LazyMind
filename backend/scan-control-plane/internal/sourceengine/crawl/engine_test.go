package crawl

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestCrawlEngineFullUsesListChildrenBFSFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	conn := newCrawlTreeConnector(false, false, nil)
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-full",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeFull,
	})
	if err != nil {
		t.Fatalf("run full crawl: %v", err)
	}
	if result.Status != RunStatusSucceeded || !result.Coverage.Complete || !result.Coverage.CoveredTargetRoot {
		t.Fatalf("unexpected full result: %+v", result)
	}
	if conn.fetchCalls != 0 || !slices.Equal(conn.listNodes, []string{"", "folder-1"}) {
		t.Fatalf("expected BFS ListChildren fallback, fetch_calls=%d list_nodes=%v", conn.fetchCalls, conn.listNodes)
	}
	assertCrawlState(t, repo.reducer, "doc-1", "NEW", "CREATE")
}

func TestCrawlEngineListChildrenIntervalOnlyAppliesBetweenCrawlListRequests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	conn := newCrawlTreeConnector(false, false, nil)
	registry, err := connector.NewDefaultConnectorRegistry(conn)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	sleeps := []time.Duration{}
	engine := NewDefaultCrawlEngine(
		repo,
		registry,
		repo,
		repo.reducer,
		WithClock(func() time.Time { return now }),
		WithPageSize(2),
		WithListRequestInterval(time.Second),
		withSleep(func(ctx context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return ctx.Err()
		}),
	)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-full-throttled",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeFull,
	})
	if err != nil {
		t.Fatalf("run full crawl: %v", err)
	}
	if result.Status != RunStatusSucceeded {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !slices.Equal(conn.listNodes, []string{"", "folder-1"}) {
		t.Fatalf("expected two crawl list calls, got %v", conn.listNodes)
	}
	if !slices.Equal(sleeps, []time.Duration{time.Second}) {
		t.Fatalf("expected one interval before second crawl list call, got %v", sleeps)
	}
}

func TestCrawlEngineDeltaUnsupportedFallsBackToFull(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	conn := newCrawlTreeConnector(true, false, nil)
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-delta",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeDelta,
		Cursor:            "delta-cursor",
	})
	if err != nil {
		t.Fatalf("run delta crawl: %v", err)
	}
	if result.Status != RunStatusSucceeded || result.Coverage.ScopeType != connector.ScopeTypeFull || !result.Coverage.CoveredTargetRoot {
		t.Fatalf("expected full fallback result, got %+v", result)
	}
	if !slices.Equal(conn.fetchScopes, []connector.ScopeType{connector.ScopeTypeFull}) {
		t.Fatalf("delta fallback should not call unsupported FetchPage(delta), scopes=%v", conn.fetchScopes)
	}
}

func TestCrawlEnginePartialAndWatchDoNotDeleteOutsideCoverage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	repo.reducer.seedState("stale-doc", "stale-v1", "stale-v1", "UNCHANGED")
	conn := newCrawlTreeConnector(true, true, nil)
	engine := newCrawlTestEngine(t, repo, conn, now)

	partial, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-partial",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypePartial,
		ScopeRef:          connector.ScopeRef{"object_key": "doc-1"},
	})
	if err != nil || partial.Status != RunStatusSucceeded {
		t.Fatalf("partial result=%+v err=%v", partial, err)
	}
	assertCrawlState(t, repo.reducer, "stale-doc", "UNCHANGED", "")

	watch, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-watch",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeWatchEvent,
		ScopeRef:          connector.ScopeRef{"object_key": "doc-1"},
	})
	if err != nil || watch.Status != RunStatusSucceeded {
		t.Fatalf("watch result=%+v err=%v", watch, err)
	}
	assertCrawlState(t, repo.reducer, "stale-doc", "UNCHANGED", "")
}

func TestCrawlEngineDeltaExplicitDeleteOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	repo.reducer.seedState("doc-deleted", "v1", "v1", "UNCHANGED")
	repo.reducer.seedState("stale-doc", "stale-v1", "stale-v1", "UNCHANGED")
	conn := newCrawlTreeConnector(true, true, []connector.RawObject{deletedRaw("doc-deleted", now)})
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-delta-delete",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeDelta,
	})
	if err != nil || result.Status != RunStatusSucceeded {
		t.Fatalf("delta result=%+v err=%v", result, err)
	}
	assertCrawlState(t, repo.reducer, "doc-deleted", "DELETED", "DELETE")
	assertCrawlState(t, repo.reducer, "stale-doc", "UNCHANGED", "")
}

func TestCrawlEngineCancelsIfGenerationChangesBeforeWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	conn := newCrawlTreeConnector(true, true, nil)
	repo.changeGenerationAfterReads = 1
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-stale",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeFull,
	})
	if err != nil {
		t.Fatalf("run stale crawl: %v", err)
	}
	if result.Status != RunStatusCanceled || result.ErrorCode != string(store.ErrCodeGenerationConflict) {
		t.Fatalf("expected stale generation cancel, got %+v", result)
	}
	if len(repo.objects) != 0 || len(repo.reducer.states) != 0 {
		t.Fatalf("stale generation wrote objects=%v states=%v", repo.objects, repo.reducer.states)
	}
}

func TestCrawlEngineFullWritesBindingRootAndNormalizesDirectChildren(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	conn := newCrawlTreeConnector(true, true, nil)
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-root",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeFull,
	})
	if err != nil || result.Status != RunStatusSucceeded {
		t.Fatalf("full result=%+v err=%v", result, err)
	}
	root := repo.objects["root"]
	if root.ObjectKey != "root" || root.ParentKey != "" || root.Depth != 0 || !root.IsContainer {
		t.Fatalf("binding root object not indexed correctly: %+v", root)
	}
	folder := repo.objects["folder-1"]
	if folder.ParentKey != "root" || folder.Depth != 1 {
		t.Fatalf("direct child parent/depth not normalized: %+v", folder)
	}
}

func TestCrawlEngineFullIndexesDualRoleBindingRootDocument(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	repo.binding.ConnectorType = string(crawlDualRoleConnectorType)
	conn := newCrawlTreeConnector(false, false, []connector.RawObject{docRaw("root", "", "root-v1")})
	conn.connectorType = crawlDualRoleConnectorType
	conn.dualRole = true
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-dual-root",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeFull,
	})
	if err != nil || result.Status != RunStatusSucceeded {
		t.Fatalf("full dual-role result=%+v err=%v", result, err)
	}
	if conn.fetchCalls != 1 || !slices.Equal(conn.fetchScopes, []connector.ScopeType{connector.ScopeTypeWatchEvent}) {
		t.Fatalf("expected one root fetch before BFS, calls=%d scopes=%v", conn.fetchCalls, conn.fetchScopes)
	}
	root := repo.objects["root"]
	if root.ObjectKey != "root" || !root.IsDocument || root.SourceVersion != "root-v1" {
		t.Fatalf("dual-role binding root document was not indexed: %+v", root)
	}
	assertCrawlState(t, repo.reducer, "root", "NEW", "CREATE")
}

func TestCrawlEnginePartialContainerRecursesChildren(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	conn := newCrawlTreeConnector(false, false, nil)
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-partial-folder",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypePartial,
		ScopeRef:          connector.ScopeRef{"object_key": "folder-1", "node_ref": "folder-1", "subtree_root": "folder-1"},
	})
	if err != nil || result.Status != RunStatusSucceeded {
		t.Fatalf("partial subtree result=%+v err=%v", result, err)
	}
	if conn.fetchCalls != 0 || !slices.Equal(conn.listNodes, []string{"folder-1"}) {
		t.Fatalf("container partial sync should traverse children without single-object fetch, fetches=%d lists=%v", conn.fetchCalls, conn.listNodes)
	}
	if _, ok := repo.objects["folder-1"]; ok {
		t.Fatalf("ordinary container itself should not be indexed by subtree sync: %+v", repo.objects["folder-1"])
	}
	if _, ok := repo.objects["doc-1"]; !ok {
		t.Fatalf("ordinary container subtree should index child document, objects=%+v", repo.objects)
	}
	assertCrawlState(t, repo.reducer, "doc-1", "NEW", "CREATE")
}

func TestCrawlEnginePartialContainerMarksMissingDescendantsDeleted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	repo.reducer.seedStateWithParent("doc-missing", "folder-1", "stale-v1", "stale-v1", "UNCHANGED")
	repo.reducer.seedStateWithParent("outside-doc", "other-folder", "outside-v1", "outside-v1", "UNCHANGED")
	conn := newCrawlTreeConnector(false, false, nil)
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-partial-delete",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypePartial,
		ScopeRef:          connector.ScopeRef{"object_key": "folder-1", "node_ref": "folder-1", "subtree_root": "folder-1"},
	})
	if err != nil || result.Status != RunStatusSucceeded {
		t.Fatalf("partial subtree result=%+v err=%v", result, err)
	}
	if result.Counts.Deleted != 1 {
		t.Fatalf("expected one missing descendant delete, got %+v", result.Counts)
	}
	assertCrawlState(t, repo.reducer, "doc-missing", "DELETED", "DELETE")
	assertCrawlState(t, repo.reducer, "outside-doc", "UNCHANGED", "")
}

func TestCrawlEnginePartialDualRoleContainerIncludesSelfAndChildren(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	repo.binding.ConnectorType = string(crawlDualRoleConnectorType)
	conn := newCrawlTreeConnector(false, false, nil)
	conn.connectorType = crawlDualRoleConnectorType
	conn.dualRole = true
	conn.fetchItems = []connector.RawObject{docRaw("folder-1", "root", "folder-v1")}
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-partial-wiki-parent",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypePartial,
		ScopeRef:          connector.ScopeRef{"object_key": "folder-1", "node_ref": "folder-1", "subtree_root": "folder-1"},
	})
	if err != nil || result.Status != RunStatusSucceeded {
		t.Fatalf("partial dual-role subtree result=%+v err=%v", result, err)
	}
	if conn.fetchCalls != 1 || !slices.Equal(conn.fetchScopes, []connector.ScopeType{connector.ScopeTypeWatchEvent}) || !slices.Equal(conn.listNodes, []string{"folder-1"}) {
		t.Fatalf("dual-role partial sync should fetch parent then traverse children, fetches=%d scopes=%v lists=%v", conn.fetchCalls, conn.fetchScopes, conn.listNodes)
	}
	if _, ok := repo.objects["folder-1"]; !ok {
		t.Fatalf("dual-role parent document should be indexed, objects=%+v", repo.objects)
	}
	if _, ok := repo.objects["doc-1"]; !ok {
		t.Fatalf("dual-role parent subtree should index child document, objects=%+v", repo.objects)
	}
	assertCrawlState(t, repo.reducer, "folder-1", "NEW", "CREATE")
	assertCrawlState(t, repo.reducer, "doc-1", "NEW", "CREATE")
}

func TestCrawlEnginePermissionDeniedMarksBindingErrorWithoutDeleting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	repo.reducer.seedState("stale-doc", "stale-v1", "stale-v1", "UNCHANGED")
	conn := newCrawlTreeConnector(true, true, nil)
	conn.fetchErr = connector.NewError(connector.ErrorCodePermissionDenied, "source denied")
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-denied",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeFull,
	})
	if err != nil {
		t.Fatalf("permission denied run: %v", err)
	}
	if result.Status != RunStatusFailed || result.ErrorCode != string(connector.ErrorCodePermissionDenied) {
		t.Fatalf("expected failed permission result, got %+v", result)
	}
	if repo.binding.Status != "ERROR" || repo.binding.LastError["code"] != string(connector.ErrorCodePermissionDenied) {
		t.Fatalf("binding was not marked error: %+v", repo.binding)
	}
	assertCrawlState(t, repo.reducer, "stale-doc", "UNCHANGED", "")
}

func TestWatchScopeRefDoesNotDeleteWithoutExplicitTombstone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := crawlTestTime()
	repo := newCrawlTestRepo(now)
	repo.reducer.seedState("doc-1", "v1", "v1", "UNCHANGED")
	conn := newCrawlTreeConnector(true, true, []connector.RawObject{})
	engine := newCrawlTestEngine(t, repo, conn, now)

	result, err := engine.Run(ctx, BindingRunClaim{
		RunID:             "run-watch-no-delete",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         connector.ScopeTypeWatchEvent,
		ScopeRef:          connector.ScopeRef{"object_key": "doc-1"},
	})
	if err != nil || result.Status != RunStatusSucceeded {
		t.Fatalf("watch result=%+v err=%v", result, err)
	}
	if len(result.Coverage.CoveredObjectKeys) != 0 {
		t.Fatalf("watch scope_ref must not imply delete coverage: %+v", result.Coverage)
	}
	assertCrawlState(t, repo.reducer, "doc-1", "UNCHANGED", "")
}

func newCrawlTestEngine(t *testing.T, repo *crawlTestRepo, conn connector.SourceConnector, now time.Time) *DefaultCrawlEngine {
	t.Helper()
	registry, err := connector.NewDefaultConnectorRegistry(conn)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return NewDefaultCrawlEngine(repo, registry, repo, repo.reducer, WithClock(func() time.Time { return now }), WithPageSize(2))
}

type crawlTestRepo struct {
	binding                    store.Binding
	objects                    map[string]store.SourceObject
	reducer                    *crawlTestReducer
	now                        time.Time
	getBindingReads            int
	changeGenerationAfterReads int
}

func newCrawlTestRepo(now time.Time) *crawlTestRepo {
	return &crawlTestRepo{
		binding: store.Binding{
			SourceID:          "source-1",
			BindingID:         "binding-1",
			ConnectorType:     string(crawlTreeConnectorType),
			TargetType:        string(crawlTreeTargetType),
			TargetRef:         "target-root",
			TreeKey:           "root",
			BindingGeneration: 1,
			Status:            BindingStatusActive,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		objects: make(map[string]store.SourceObject),
		reducer: newCrawlTestReducer(now),
		now:     now,
	}
}

func (r *crawlTestRepo) GetBinding(_ context.Context, sourceID, bindingID string) (store.Binding, error) {
	if r.binding.SourceID != sourceID || r.binding.BindingID != bindingID {
		return store.Binding{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
	}
	r.getBindingReads++
	if r.changeGenerationAfterReads > 0 && r.getBindingReads > r.changeGenerationAfterReads {
		next := r.binding
		next.BindingGeneration++
		return next, nil
	}
	return r.binding, nil
}

func (r *crawlTestRepo) UpsertObjects(_ context.Context, objects []store.SourceObject) error {
	for _, object := range objects {
		r.objects[object.ObjectKey] = object
		r.reducer.parents[object.ObjectKey] = object.ParentKey
	}
	return nil
}

func (r *crawlTestRepo) MarkBindingError(_ context.Context, sourceID, bindingID string, generation int64, lastError store.JSON, now time.Time) error {
	if r.binding.SourceID != sourceID || r.binding.BindingID != bindingID {
		return store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
	}
	if r.binding.BindingGeneration != generation {
		return store.NewStoreError(store.ErrCodeGenerationConflict, "binding generation is stale")
	}
	r.binding.Status = "ERROR"
	r.binding.LastError = lastError
	r.binding.UpdatedAt = now
	return nil
}

func assertCrawlState(t *testing.T, reducer *crawlTestReducer, objectKey, sourceState, action string) {
	t.Helper()
	state, ok := reducer.states[objectKey]
	if !ok {
		t.Fatalf("state %q not found", objectKey)
	}
	if state.SourceState != sourceState || state.PendingAction != action {
		t.Fatalf("unexpected state %q: %+v", objectKey, state)
	}
}

type crawlTestReducer struct {
	states  map[string]store.DocumentState
	parents map[string]string
	now     time.Time
}

func newCrawlTestReducer(now time.Time) *crawlTestReducer {
	return &crawlTestReducer{
		states:  make(map[string]store.DocumentState),
		parents: make(map[string]string),
		now:     now,
	}
}

func (r *crawlTestReducer) ReduceSeenObjects(_ context.Context, input ReduceSeenInput) (ReduceSeenResult, error) {
	result := ReduceSeenResult{}
	for _, object := range input.Objects {
		if !object.IsDocument {
			continue
		}
		state := r.states[object.ObjectKey]
		state.SourceID = input.SourceID
		state.BindingID = input.BindingID
		state.BindingGeneration = input.BindingGeneration
		state.ObjectKey = object.ObjectKey
		state.SourceVersion = object.SourceVersion
		state.DeletedAtSource = object.DeletedAtSource
		state.DocumentListVisible = true
		state.Selectable = true
		state.CreatedAt = firstTime(state.CreatedAt, r.now)
		state.UpdatedAt = r.now
		if object.DeletedAtSource != nil {
			state.SourceState = "DELETED"
			state.PendingAction = "DELETE"
			result.DeletedCount++
		} else if state.BaselineVersion == "" {
			state.SourceState = "NEW"
			state.PendingAction = "CREATE"
			result.NewCount++
		} else if state.BaselineVersion == object.SourceVersion {
			state.SourceState = "UNCHANGED"
			state.PendingAction = ""
			result.UnchangedCount++
		} else {
			state.SourceState = "MODIFIED"
			state.PendingAction = "REPARSE"
			result.ModifiedCount++
		}
		r.states[object.ObjectKey] = state
	}
	return result, nil
}

func (r *crawlTestReducer) ReduceMissingObjects(_ context.Context, input ReduceMissingInput) (ReduceMissingResult, error) {
	if !input.RunSucceeded || !input.Coverage.Complete {
		return ReduceMissingResult{}, nil
	}
	seen := map[string]struct{}{}
	for _, key := range input.SeenObjectKeys {
		seen[key] = struct{}{}
	}
	result := ReduceMissingResult{}
	for key, state := range r.states {
		if _, ok := seen[key]; ok || state.SourceState == "DELETED" {
			continue
		}
		if !r.testCoverageContains(input.Coverage, key) {
			continue
		}
		state.SourceState = "DELETED"
		state.PendingAction = "DELETE"
		state.UpdatedAt = r.now
		r.states[key] = state
		result.DeletedCount++
		result.AffectedObjectKeys = append(result.AffectedObjectKeys, key)
	}
	return result, nil
}

func (r *crawlTestReducer) seedState(objectKey, sourceVersion, baseline, sourceState string) {
	r.seedStateWithParent(objectKey, "", sourceVersion, baseline, sourceState)
}

func (r *crawlTestReducer) seedStateWithParent(objectKey, parentKey, sourceVersion, baseline, sourceState string) {
	r.states[objectKey] = store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           objectKey,
		SourceVersion:       sourceVersion,
		BaselineVersion:     baseline,
		SourceState:         sourceState,
		DocumentListVisible: true,
		Selectable:          true,
		CreatedAt:           r.now,
		UpdatedAt:           r.now,
	}
	r.parents[objectKey] = parentKey
}

func (r *crawlTestReducer) testCoverageContains(coverage Coverage, objectKey string) bool {
	if coverage.ScopeType == connector.ScopeTypeFull {
		return coverage.CoveredTargetRoot
	}
	if slices.Contains(coverage.CoveredObjectKeys, objectKey) || slices.Contains(coverage.CoveredSubtrees, objectKey) {
		return true
	}
	if coverage.ScopeType != connector.ScopeTypePartial {
		return false
	}
	visited := map[string]struct{}{}
	for key := objectKey; key != ""; key = r.parents[key] {
		if _, ok := visited[key]; ok {
			return false
		}
		visited[key] = struct{}{}
		if slices.Contains(coverage.CoveredSubtrees, key) {
			return true
		}
	}
	return false
}

func firstTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

type crawlTreeConnector struct {
	connectorType connector.ConnectorType
	recursive     bool
	delta         bool
	dualRole      bool
	deltaItems    []connector.RawObject
	fetchItems    []connector.RawObject
	fetchErr      error
	fetchCalls    int
	fetchScopes   []connector.ScopeType
	listNodes     []string
}

const (
	crawlTreeConnectorType     connector.ConnectorType = "crawl_tree"
	crawlDualRoleConnectorType connector.ConnectorType = "crawl_dual_role_tree"
	crawlTreeTargetType        connector.TargetType    = "crawl_tree_root"
)

func newCrawlTreeConnector(recursive, delta bool, deltaItems []connector.RawObject) *crawlTreeConnector {
	return &crawlTreeConnector{recursive: recursive, delta: delta, deltaItems: deltaItems}
}

func (c *crawlTreeConnector) Spec() connector.ConnectorSpec {
	connectorType := c.connectorType
	if connectorType == "" {
		connectorType = crawlTreeConnectorType
	}
	return connector.ConnectorSpec{
		ConnectorType:          connectorType,
		TargetTypes:            []connector.TargetType{crawlTreeTargetType},
		SupportsDelta:          c.delta,
		SupportsRecursiveFetch: c.recursive,
		SupportsDualRoleObject: c.dualRole,
		MaxPageSize:            100,
	}
}

func (c *crawlTreeConnector) ValidateTarget(context.Context, connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	return connector.NormalizedTarget{}, nil
}

func (c *crawlTreeConnector) ListChildren(_ context.Context, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	c.listNodes = append(c.listNodes, req.NodeRef)
	switch req.NodeRef {
	case "":
		return connector.RawObjectPage{Items: []connector.RawObject{folderRaw("folder-1")}}, nil
	case "folder-1":
		return connector.RawObjectPage{Items: []connector.RawObject{docRaw("doc-1", "folder-1", "v1")}}, nil
	default:
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeNotFound, "node not found")
	}
}

func (c *crawlTreeConnector) Search(context.Context, connector.SearchRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "search unsupported")
}

func (c *crawlTreeConnector) FetchPage(_ context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	c.fetchCalls++
	c.fetchScopes = append(c.fetchScopes, req.ScopeType)
	if c.fetchErr != nil {
		return connector.RawObjectPage{}, c.fetchErr
	}
	if req.ScopeType == connector.ScopeTypeDelta {
		if !c.delta {
			return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupportedDelta, "delta unsupported")
		}
		return connector.RawObjectPage{Items: c.deltaItems, Watermark: "delta-2"}, nil
	}
	if req.ScopeType == connector.ScopeTypePartial || req.ScopeType == connector.ScopeTypeWatchEvent {
		if c.fetchItems != nil {
			return connector.RawObjectPage{Items: c.fetchItems}, nil
		}
		if c.deltaItems != nil {
			return connector.RawObjectPage{Items: c.deltaItems}, nil
		}
		return connector.RawObjectPage{Items: []connector.RawObject{docRaw("doc-1", "folder-1", "v1")}}, nil
	}
	return connector.RawObjectPage{Items: []connector.RawObject{folderRaw("folder-1"), docRaw("doc-1", "folder-1", "v1")}, Watermark: "full-1"}, nil
}

func (c *crawlTreeConnector) ExportObject(context.Context, connector.ExportObjectRequest) (connector.ExportedObject, error) {
	return connector.ExportedObject{}, errors.New("not implemented")
}

func (c *crawlTreeConnector) MapObject(_ context.Context, raw connector.RawObject) (connector.NormalizedSourceObject, error) {
	return connector.NormalizedSourceObject{
		ObjectKey:       raw.ObjectKey,
		ParentKey:       raw.ParentKey,
		DisplayName:     raw.DisplayName,
		SearchName:      raw.SearchName,
		ObjectType:      raw.ObjectType,
		IsDocument:      raw.IsDocument,
		IsContainer:     raw.IsContainer,
		HasChildren:     raw.HasChildren,
		SourceVersion:   raw.SourceVersion,
		DeletedAtSource: raw.DeletedAtSource,
	}, nil
}

func folderRaw(key string) connector.RawObject {
	return connector.RawObject{
		ObjectRef:     key,
		ObjectKey:     key,
		ParentKey:     "root",
		DisplayName:   key,
		SearchName:    key,
		ObjectType:    connector.ObjectTypeFolder,
		IsContainer:   true,
		HasChildren:   true,
		SourceVersion: "folder-v1",
	}
}

func docRaw(key, parent, version string) connector.RawObject {
	return connector.RawObject{
		ObjectRef:       key,
		ObjectKey:       key,
		ParentKey:       parent,
		DisplayName:     key,
		SearchName:      key,
		ObjectType:      connector.ObjectTypeFile,
		IsDocument:      true,
		SourceVersion:   version,
		MimeType:        "text/markdown",
		FileExtension:   ".md",
		DeletedAtSource: nil,
	}
}

func deletedRaw(key string, deletedAt time.Time) connector.RawObject {
	raw := docRaw(key, "root", "v1")
	raw.DeletedAtSource = &deletedAt
	return raw
}

func crawlTestTime() time.Time {
	return time.Date(2026, 5, 28, 9, 0, 0, 0, time.UTC)
}
