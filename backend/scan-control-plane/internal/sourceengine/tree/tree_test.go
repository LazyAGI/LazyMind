package tree

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestTargetTreeListChildrenUsesConnectorAndDoesNotUseFallbackOrStore(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	fallback := &panicFallbackSearch{t: t}
	engine := NewDefaultTargetTreeEngine(registry, WithFallbackSearch(fallback), WithTargetTreeLimits(TreeQueryLimits{DefaultPageSize: 2, MaxPageSize: 2, MaxAllCurrentLevelItems: 10}))

	page, err := engine.ListChildren(context.Background(), TargetTreeChildrenRequest{
		ConnectorType: treeTestConnectorType,
		TargetType:    treeTestTargetType,
		TargetRef:     "tree-test://root",
		IncludeFiles:  true,
		PageSize:      2,
	})
	if err != nil {
		t.Fatalf("list target children: %v", err)
	}
	if len(spy.listRequests) != 1 || len(spy.mapObjects) != 2 {
		t.Fatalf("expected connector list and map calls, list=%d map=%d", len(spy.listRequests), len(spy.mapObjects))
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected target directory tree to hide files, got %+v", page.Items)
	}
	if page.Items[0].ObjectKey != "folder-1" {
		t.Fatalf("unexpected target tree nodes: %+v", page.Items)
	}
	if fallback.called {
		t.Fatalf("target children should not use fallback search")
	}
}

func TestTargetTreeAllCurrentLevelPullsPagesWithoutWritingBusinessTables(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry, WithTargetTreeLimits(TreeQueryLimits{DefaultPageSize: 2, MaxPageSize: 2, MaxAllCurrentLevelItems: 10}))

	page, err := engine.ListChildren(context.Background(), TargetTreeChildrenRequest{
		ConnectorType: treeTestConnectorType,
		TargetType:    treeTestTargetType,
		TargetRef:     "tree-test://root",
		IncludeFiles:  true,
		ListMode:      ListModeAllCurrentLevel,
		MaxItems:      10,
		PageSize:      2,
	})
	if err != nil {
		t.Fatalf("list all current level: %v", err)
	}
	if len(spy.listRequests) != 2 {
		t.Fatalf("expected connector pagination, got %d requests", len(spy.listRequests))
	}
	if !page.ListComplete || page.HasMore || len(page.Items) != 2 {
		t.Fatalf("expected complete current-level directory page, got %+v", page)
	}
	if page.Items[0].ObjectKey != "folder-1" || page.Items[1].ObjectKey != "page-1" {
		t.Fatalf("target directory tree should keep containers and hide files, got %+v", page.Items)
	}
}

func TestTargetTreeSearchFallsBackToIndexedReadOnlySearch(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{supportsSearch: false}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.objects = []ObjectWithState{indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Handbook.md", true, false)}
	engine := NewDefaultTargetTreeEngine(registry, WithFallbackSearch(NewIndexedTargetTreeFallbackSearch(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})))

	page, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "hand",
		TargetRef:     "binding-1",
		PageSize:      10,
	})
	if err != nil {
		t.Fatalf("fallback search: %v", err)
	}
	if len(spy.searchRequests) != 0 {
		t.Fatalf("connector search should not be called for unsupported search")
	}
	if repo.searchObjectsCalls != 1 {
		t.Fatalf("fallback should read indexed objects exactly once, got %d", repo.searchObjectsCalls)
	}
	if page.SearchMode != SearchModeFallback || len(page.Items) != 1 || page.Items[0].ObjectKey != "doc-1" {
		t.Fatalf("unexpected fallback page: %+v", page)
	}
}

func TestTargetTreeFallbackSearchScopesToBindingAndTreeKey(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{supportsSearch: false}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-a", "tree-a", "doc-a", "", "Handbook.md", true, false),
		indexedObject("source-1", "binding-b", "tree-b", "doc-b", "", "Handbook.md", true, false),
	}
	engine := NewDefaultTargetTreeEngine(registry, WithFallbackSearch(NewIndexedTargetTreeFallbackSearch(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})))

	page, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		TargetRef:     "ignored-target-ref",
		Keyword:       "hand",
		PageSize:      10,
		ProviderOptions: map[string]any{
			"binding_id": "binding-a",
			"tree_key":   "tree-a",
		},
	})
	if err != nil {
		t.Fatalf("fallback search: %v", err)
	}
	if repo.lastSearch.BindingID != "binding-a" || repo.lastSearch.TreeKey != "tree-a" {
		t.Fatalf("fallback search was not scoped to binding/tree: %+v", repo.lastSearch)
	}
	if len(page.Items) != 1 || page.Items[0].BindingID != "binding-a" {
		t.Fatalf("fallback search crossed binding scope: %+v", page.Items)
	}
}

func TestTargetTreeSearchRespectsIncludeFiles(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{supportsSearch: true}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry)

	withoutFiles, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		TargetType:    treeTestTargetType,
		TargetRef:     "tree-test://root",
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  false,
	})
	if err != nil {
		t.Fatalf("search target tree without files: %v", err)
	}
	if len(withoutFiles.Items) != 0 {
		t.Fatalf("search should filter files when include_files=false, got %+v", withoutFiles.Items)
	}
	if len(spy.searchRequests) != 0 || len(spy.listRequests) != 1 {
		t.Fatalf("target search should filter normal list results without connector search, searches=%d lists=%d", len(spy.searchRequests), len(spy.listRequests))
	}
	if spy.listRequests[0].Cursor != "" || spy.listRequests[0].PageSize != 2 {
		t.Fatalf("search should pass normal list pagination, got %+v", spy.listRequests[0])
	}

	withFiles, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		TargetType:    treeTestTargetType,
		TargetRef:     "tree-test://root",
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	})
	if err != nil {
		t.Fatalf("search target tree with files: %v", err)
	}
	if len(withFiles.Items) != 1 || withFiles.Items[0].ObjectKey != "doc-1" {
		t.Fatalf("search should keep files when include_files=true, got %+v", withFiles.Items)
	}
	if len(spy.searchRequests) != 0 || len(spy.listRequests) != 2 {
		t.Fatalf("target search should continue using normal list results, searches=%d lists=%d", len(spy.searchRequests), len(spy.listRequests))
	}
}

func TestLocalFSTargetSearchWithCurrentLevelBuildsRecursiveCache(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{
		connectorType:  connector.ConnectorType("local_fs"),
		supportsSearch: true,
		childrenByNodeRef: map[string][]connector.RawObject{
			"/workspace/docs": {
				rawTreeObject("/workspace/docs/guides", "", "Guides", false, true),
				rawTreeObject("/workspace/docs/readme.md", "", "Readme.md", true, false),
			},
			"/workspace/docs/guides": {
				rawTreeObject("/workspace/docs/guides/test-plan.md", "/workspace/docs/guides", "test-plan.md", true, false),
			},
		},
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry)
	engine.cache.delay = time.Hour

	page, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: connector.ConnectorType("local_fs"),
		TargetType:    connector.TargetType("local_path"),
		TargetRef:     "/workspace/docs",
		Keyword:       "test",
		PageSize:      10,
		IncludeFiles:  true,
	})
	if err != nil {
		t.Fatalf("search local recursive cache: %v", err)
	}
	if page.SearchMode != SearchModeCache || page.CacheStatus != targetSearchCacheStatusComplete || !page.CacheComplete {
		t.Fatalf("local current-level search should build and read cache, got %+v", page)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "/workspace/docs/guides/test-plan.md" {
		t.Fatalf("local recursive search should find nested match, got %+v", page.Items)
	}
	if len(spy.searchRequests) != 0 || len(spy.listRequests) != 2 {
		t.Fatalf("local recursive search should list subtree without connector search, searches=%d lists=%d", len(spy.searchRequests), len(spy.listRequests))
	}
}

func TestLocalFSTargetSearchWithoutCurrentLevelBuildsRootCaches(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{
		connectorType:  connector.ConnectorType("local_fs"),
		supportsSearch: true,
		childrenByNodeRef: map[string][]connector.RawObject{
			"": {
				rawTreeObject("/workspace/docs", "", "docs", false, true),
			},
			"/workspace/docs": {
				rawTreeObject("/workspace/docs/guides", "/workspace/docs", "Guides", false, true),
			},
			"/workspace/docs/guides": {
				rawTreeObject("/workspace/docs/guides/test-plan.md", "/workspace/docs/guides", "test-plan.md", true, false),
			},
		},
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry)

	page, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: connector.ConnectorType("local_fs"),
		TargetType:    connector.TargetType("local_path"),
		Keyword:       "test",
		PageSize:      10,
		IncludeFiles:  true,
	})
	if err != nil {
		t.Fatalf("search local roots: %v", err)
	}
	if page.SearchMode != SearchModeCache || page.CacheStatus != targetSearchCacheStatusComplete || !page.CacheComplete {
		t.Fatalf("local search without current level should build root caches, got %+v", page)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "/workspace/docs/guides/test-plan.md" {
		t.Fatalf("local root cache search should find nested match, got %+v", page.Items)
	}
	if len(spy.searchRequests) != 0 || len(spy.listRequests) != 3 {
		t.Fatalf("local search without current level should list recommended roots and subtree, searches=%d lists=%d", len(spy.searchRequests), len(spy.listRequests))
	}
}

func TestTargetTreeSearchWithoutCurrentLevelUsesCache(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{supportsSearch: true}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry)
	engine.cache.delay = 0

	first, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	})
	if err != nil {
		t.Fatalf("cached search first response: %v", err)
	}
	if first.SearchMode != SearchModeCache || first.CacheStatus != targetSearchCacheStatusMissing || first.CacheBuilding || len(first.Items) != 0 {
		t.Fatalf("first cached search should only read missing cache, got %+v", first)
	}
	if len(spy.searchRequests) != 0 || len(spy.listRequests) != 0 {
		t.Fatalf("cache miss search should not access connector, searches=%d lists=%d", len(spy.searchRequests), len(spy.listRequests))
	}

	if err := engine.Prewarm(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		IncludeFiles:  true,
	}); err != nil {
		t.Fatalf("prewarm target search cache: %v", err)
	}

	page, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	})
	if err != nil {
		t.Fatalf("cached search: %v", err)
	}
	if page.CacheStatus != targetSearchCacheStatusComplete || page.CacheBuilding || !page.CacheComplete || len(page.Items) != 1 || page.Items[0].ObjectKey != "doc-1" {
		t.Fatalf("cached search should return completed cache matches, got %+v", page)
	}
	if len(spy.searchRequests) != 0 || len(spy.listRequests) == 0 {
		t.Fatalf("prewarm should build from normal list calls only, searches=%d lists=%d", len(spy.searchRequests), len(spy.listRequests))
	}
}

func TestTargetTreeSearchCachePersistsThroughStore(t *testing.T) {
	t.Parallel()

	store := newMemoryTargetSearchCacheStore()
	buildSpy := &treeConnectorSpy{supportsSearch: true}
	buildRegistry, err := connector.NewDefaultConnectorRegistry(buildSpy)
	if err != nil {
		t.Fatalf("create build registry: %v", err)
	}
	buildEngine := NewDefaultTargetTreeEngine(buildRegistry, WithTargetSearchCacheStore(store))
	buildEngine.cache.delay = 0
	req := TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	}
	if err := buildEngine.Prewarm(context.Background(), req); err != nil {
		t.Fatalf("prewarm target search cache: %v", err)
	}
	if store.locks != 1 || store.sets != 1 {
		t.Fatalf("prewarm should lock and persist cache once, locks=%d sets=%d", store.locks, store.sets)
	}

	readSpy := &treeConnectorSpy{supportsSearch: true}
	readRegistry, err := connector.NewDefaultConnectorRegistry(readSpy)
	if err != nil {
		t.Fatalf("create read registry: %v", err)
	}
	readEngine := NewDefaultTargetTreeEngine(readRegistry, WithTargetSearchCacheStore(store))
	page, err := readEngine.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("cached search from store: %v", err)
	}
	if page.CacheStatus != targetSearchCacheStatusComplete || !page.CacheComplete || len(page.Items) != 1 || page.Items[0].ObjectKey != "doc-1" {
		t.Fatalf("search should read completed cache from store, got %+v", page)
	}
	if len(readSpy.listRequests) != 0 || len(readSpy.searchRequests) != 0 {
		t.Fatalf("store-backed search should not access connector, lists=%d searches=%d", len(readSpy.listRequests), len(readSpy.searchRequests))
	}
}

func TestTargetTreeSearchReturnsStaleStoreCacheUntilPrewarmRefreshes(t *testing.T) {
	t.Parallel()

	store := newMemoryTargetSearchCacheStore()
	buildSpy := &treeConnectorSpy{supportsSearch: true}
	buildRegistry, err := connector.NewDefaultConnectorRegistry(buildSpy)
	if err != nil {
		t.Fatalf("create build registry: %v", err)
	}
	req := TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	}
	buildEngine := NewDefaultTargetTreeEngine(buildRegistry, WithTargetSearchCacheStore(store))
	buildEngine.cache.delay = 0
	if err := buildEngine.Prewarm(context.Background(), req); err != nil {
		t.Fatalf("prewarm target search cache: %v", err)
	}
	key := targetSearchCacheKey(req)
	snapshot := store.snapshots[key]
	snapshot.staleAt = time.Now().Add(-time.Second)
	snapshot.stale = true
	store.snapshots[key] = snapshot

	readSpy := &treeConnectorSpy{supportsSearch: true}
	readRegistry, err := connector.NewDefaultConnectorRegistry(readSpy)
	if err != nil {
		t.Fatalf("create read registry: %v", err)
	}
	readEngine := NewDefaultTargetTreeEngine(readRegistry, WithTargetSearchCacheStore(store))
	page, err := readEngine.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("search stale cache from store: %v", err)
	}
	if page.CacheStatus != targetSearchCacheStatusComplete || !page.CacheComplete || len(page.Items) != 1 || page.Items[0].ObjectKey != "doc-1" {
		t.Fatalf("search should keep returning stale completed cache, got %+v", page)
	}
	if len(readSpy.listRequests) != 0 || len(readSpy.searchRequests) != 0 {
		t.Fatalf("search should only read stale cache, lists=%d searches=%d", len(readSpy.listRequests), len(readSpy.searchRequests))
	}

	refreshSpy := &treeConnectorSpy{
		supportsSearch: true,
		childrenSet:    true,
		children:       []connector.RawObject{rawTreeObject("doc-2", "", "Updated.md", true, false)},
	}
	refreshRegistry, err := connector.NewDefaultConnectorRegistry(refreshSpy)
	if err != nil {
		t.Fatalf("create refresh registry: %v", err)
	}
	refreshEngine := NewDefaultTargetTreeEngine(refreshRegistry, WithTargetSearchCacheStore(store))
	refreshEngine.cache.delay = 0
	if err := refreshEngine.Prewarm(context.Background(), req); err != nil {
		t.Fatalf("refresh stale target search cache: %v", err)
	}
	if store.sets != 2 || len(refreshSpy.listRequests) == 0 {
		t.Fatalf("stale cache should be rebuilt once, sets=%d lists=%d", store.sets, len(refreshSpy.listRequests))
	}

	updatedReq := req
	updatedReq.Keyword = "updated"
	page, err = readEngine.Search(context.Background(), updatedReq)
	if err != nil {
		t.Fatalf("search refreshed cache from store: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "doc-2" || page.CacheError != "" {
		t.Fatalf("search should read refreshed cache, got %+v", page)
	}
}

func TestTargetTreeSearchCacheFailedRefreshPreservesPreviousCompleteSnapshot(t *testing.T) {
	t.Parallel()

	store := newMemoryTargetSearchCacheStore()
	buildSpy := &treeConnectorSpy{supportsSearch: true}
	buildRegistry, err := connector.NewDefaultConnectorRegistry(buildSpy)
	if err != nil {
		t.Fatalf("create build registry: %v", err)
	}
	req := TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	}
	buildEngine := NewDefaultTargetTreeEngine(buildRegistry, WithTargetSearchCacheStore(store))
	buildEngine.cache.delay = 0
	if err := buildEngine.Prewarm(context.Background(), req); err != nil {
		t.Fatalf("prewarm target search cache: %v", err)
	}
	key := targetSearchCacheKey(req)
	snapshot := store.snapshots[key]
	snapshot.staleAt = time.Now().Add(-time.Second)
	snapshot.stale = true
	store.snapshots[key] = snapshot

	failSpy := &treeConnectorSpy{
		supportsSearch: true,
		listErr:        connector.NewError(connector.ErrorCodePermissionDenied, "permission denied"),
	}
	failRegistry, err := connector.NewDefaultConnectorRegistry(failSpy)
	if err != nil {
		t.Fatalf("create failed refresh registry: %v", err)
	}
	failEngine := NewDefaultTargetTreeEngine(failRegistry, WithTargetSearchCacheStore(store))
	failEngine.cache.delay = 0
	if err := failEngine.Prewarm(context.Background(), req); err != nil {
		t.Fatalf("failed refresh with previous complete cache should keep cache readable: %v", err)
	}

	page, err := buildEngine.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("search preserved cache after failed refresh: %v", err)
	}
	if page.CacheStatus != targetSearchCacheStatusComplete || !page.CacheComplete || len(page.Items) != 1 || page.Items[0].ObjectKey != "doc-1" {
		t.Fatalf("failed refresh should preserve previous nodes, got %+v", page)
	}
	if !strings.Contains(page.CacheError, "permission denied") {
		t.Fatalf("failed refresh should expose last error, got %+v", page)
	}
}

func TestLocalFSTargetSearchCacheUsesLongerStaleTTL(t *testing.T) {
	t.Parallel()

	store := newMemoryTargetSearchCacheStore()
	spy := &treeConnectorSpy{
		connectorType:  connector.ConnectorType("local_fs"),
		supportsSearch: true,
		childrenByNodeRef: map[string][]connector.RawObject{
			"/workspace/docs": {
				rawTreeObject("/workspace/docs/readme.md", "/workspace/docs", "Readme.md", true, false),
			},
		},
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry, WithTargetSearchCacheStore(store))
	before := time.Now()
	req := TargetTreeSearchRequest{
		ConnectorType: connector.ConnectorType("local_fs"),
		TargetType:    connector.TargetType("local_path"),
		TargetRef:     "/workspace/docs",
		IncludeFiles:  true,
	}
	if err := engine.Prewarm(context.Background(), req); err != nil {
		t.Fatalf("prewarm local_fs target search cache: %v", err)
	}
	snapshot, ok := store.snapshots[targetSearchCacheKey(req)]
	if !ok {
		t.Fatalf("local_fs cache snapshot was not persisted")
	}
	minStaleAt := before.Add(targetSearchCacheLocalFSTTL - time.Second)
	if snapshot.staleAt.Before(minStaleAt) {
		t.Fatalf("local_fs cache should use longer stale ttl, stale_at=%s min=%s", snapshot.staleAt, minStaleAt)
	}
}

func TestLocalFSRootCachePrewarmBuildsCachesSearchCanReuse(t *testing.T) {
	t.Parallel()

	store := newMemoryTargetSearchCacheStore()
	spy := &treeConnectorSpy{
		connectorType:  connector.ConnectorType("local_fs"),
		supportsSearch: true,
		childrenByNodeRef: map[string][]connector.RawObject{
			"": {
				rawTreeObject("/workspace/docs", "", "docs", false, true),
			},
			"/workspace/docs": {
				rawTreeObject("/workspace/docs/guides", "/workspace/docs", "Guides", false, true),
			},
			"/workspace/docs/guides": {
				rawTreeObject("/workspace/docs/guides/test-plan.md", "/workspace/docs/guides", "test-plan.md", true, false),
			},
		},
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry, WithTargetSearchCacheStore(store))
	engine.cache.delay = 0
	req := TargetTreeSearchRequest{
		ConnectorType: connector.ConnectorType("local_fs"),
		TargetType:    connector.TargetType("local_path"),
		IncludeFiles:  true,
	}
	if err := engine.PrewarmLocalFSRootCaches(context.Background(), req); err != nil {
		t.Fatalf("prewarm local_fs root caches: %v", err)
	}
	if len(spy.listRequests) != 3 {
		t.Fatalf("prewarm should list roots and subtree once, got %d requests", len(spy.listRequests))
	}

	spy.listRequests = nil
	page, err := engine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: connector.ConnectorType("local_fs"),
		TargetType:    connector.TargetType("local_path"),
		Keyword:       "test",
		PageSize:      10,
		IncludeFiles:  true,
	})
	if err != nil {
		t.Fatalf("search local_fs root cache: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "/workspace/docs/guides/test-plan.md" {
		t.Fatalf("search should reuse prewarmed root cache, got %+v", page)
	}
	if len(spy.listRequests) != 1 {
		t.Fatalf("search should only refresh root list and reuse subtree cache, got %d list requests", len(spy.listRequests))
	}
}

func TestTargetTreeSearchCacheFailureIsReadableAndRetryable(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{
		supportsSearch: true,
		listErr:        connector.NewError(connector.ErrorCodePermissionDenied, "permission denied"),
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry)
	engine.cache.delay = 0

	req := TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	}
	if err := engine.Prewarm(context.Background(), req); err == nil {
		t.Fatalf("failed prewarm should return an error")
	}
	page, err := engine.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("cached search after failed prewarm: %v", err)
	}
	if page.CacheStatus != targetSearchCacheStatusFailed || page.CacheError == "" || page.CacheBuilding {
		t.Fatalf("failed prewarm should leave readable failed cache state, got %+v", page)
	}

	spy.listErr = nil
	if err := engine.Prewarm(context.Background(), req); err != nil {
		t.Fatalf("retry prewarm target search cache: %v", err)
	}
	page, err = engine.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("cached search after retry: %v", err)
	}
	if page.CacheStatus != targetSearchCacheStatusComplete || !page.CacheComplete || len(page.Items) != 1 {
		t.Fatalf("retry prewarm should replace failed state with complete cache, got %+v", page)
	}
}

func TestTargetTreeSearchCacheFailsWhenPaginationCursorDoesNotAdvance(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{
		supportsSearch: true,
		repeatCursor:   true,
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry)
	engine.cache.delay = 0

	req := TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "welcome",
		PageSize:      10,
		IncludeFiles:  true,
	}
	if err := engine.Prewarm(context.Background(), req); err == nil {
		t.Fatalf("prewarm should fail when pagination cursor does not advance")
	}
	page, err := engine.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("cached search after pagination failure: %v", err)
	}
	if page.CacheStatus != targetSearchCacheStatusFailed || !strings.Contains(page.CacheError, "cursor did not advance") {
		t.Fatalf("pagination failure should leave readable failed cache state, got %+v", page)
	}
}

func TestTreeSearchRejectsUnsupportedListMode(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	targetEngine := NewDefaultTargetTreeEngine(registry)
	_, err = targetEngine.Search(context.Background(), TargetTreeSearchRequest{
		ConnectorType: treeTestConnectorType,
		Keyword:       "hand",
		ListMode:      ListModeAllCurrentLevel,
		MaxItems:      10,
	})
	assertTreeErrorCode(t, err, ErrCodeUnsupportedListMode)

	sourceEngine := NewDBSourceTreeQueryEngine(newTreeReadRepo(), TreeQueryLimits{})
	_, err = sourceEngine.Search(context.Background(), SourceTreeSearchRequest{
		SourceID: "source-1",
		Keyword:  "hand",
		ListMode: ListModeAllCurrentLevel,
		MaxItems: 10,
	})
	assertTreeErrorCode(t, err, ErrCodeUnsupportedListMode)
}

func TestTargetTreeNodeUsesBindingTargetSemantics(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{children: []connector.RawObject{{
		ObjectRef:         "/workspace/docs",
		ObjectKey:         "local_fs:agent-1:path:/workspace/docs",
		DisplayName:       "docs",
		ObjectType:        connector.ObjectTypeFolder,
		IsContainer:       true,
		HasChildren:       true,
		Bindable:          true,
		BindingTargetType: "local_path",
		BindingTargetRef:  "/workspace/docs",
		TreeKey:           "local_fs:agent-1:path:/workspace/docs",
	}}}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	engine := NewDefaultTargetTreeEngine(registry)
	page, err := engine.ListChildren(context.Background(), TargetTreeChildrenRequest{
		ConnectorType: treeTestConnectorType,
		TargetType:    treeTestTargetType,
		TargetRef:     "tree-test://root",
		PageSize:      10,
	})
	if err != nil {
		t.Fatalf("list target children: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one item, got %+v", page.Items)
	}
	node := page.Items[0]
	if node.TargetType != "local_path" || node.TargetRef != "/workspace/docs" || node.TreeKey == "" || !node.Selectable {
		t.Fatalf("node did not expose binding target semantics: %+v", node)
	}
}

func TestSourceTreeListChildrenUsesLiveConnectorWhenUseCacheFalse(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:        "binding-1",
		SourceID:         "source-1",
		TreeKey:          "tree-root",
		ConnectorType:    string(treeTestConnectorType),
		TargetType:       string(treeTestTargetType),
		TargetRef:        "tree-test://root",
		AgentID:          "agent-1",
		AuthConnectionID: "auth-1",
		ProviderOptions:  store.JSON{"existing": "kept"},
		Status:           "ACTIVE",
	}}
	engine := NewDBSourceTreeQueryEngine(
		repo,
		TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10},
		WithSourceTreeConnectorRegistry(registry),
	)

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:        "source-1",
		BindingID:       "binding-1",
		UseCache:        boolPtr(false),
		ProviderOptions: map[string]any{"user_id": "user-1"},
		PageSize:        10,
	})
	if err != nil {
		t.Fatalf("list live children: %v", err)
	}
	if repo.listObjectsCalls != 0 || len(spy.listRequests) != 1 || len(spy.mapObjects) != 2 {
		t.Fatalf("expected live connector only, listObjects=%d list=%d map=%d", repo.listObjectsCalls, len(spy.listRequests), len(spy.mapObjects))
	}
	gotReq := spy.listRequests[0]
	if gotReq.TargetType != treeTestTargetType || gotReq.TargetRef != "tree-test://root" || gotReq.AgentID != "agent-1" || gotReq.AuthConnectionID != "auth-1" {
		t.Fatalf("binding target fields were not passed to connector: %+v", gotReq)
	}
	if gotReq.ProviderOptions.String("existing") != "kept" || gotReq.ProviderOptions.String("user_id") != "user-1" {
		t.Fatalf("provider options were not merged: %+v", gotReq.ProviderOptions)
	}
	if page.SearchMode != SearchModeConnector || !page.HasMore || len(page.Items) != 2 || page.Items[1].Key != "binding-1:doc-1" || !page.Items[1].Selectable {
		t.Fatalf("unexpected live page: %+v", page)
	}
}

func TestSourceTreeLiveRootRequestUsesTreeKeyNodeRef(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{connectorType: connector.ConnectorType("feishu")}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:        "binding-1",
		SourceID:         "source-1",
		TreeKey:          "feishu:wiki:space-1:node-root",
		ConnectorType:    "feishu",
		TargetType:       string(treeTestTargetType),
		TargetRef:        "wiki:space-1:node-root",
		AuthConnectionID: "auth-1",
		Status:           "ACTIVE",
	}}
	engine := NewDBSourceTreeQueryEngine(
		repo,
		TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 100, MaxAllCurrentLevelItems: 10},
		WithSourceTreeConnectorRegistry(registry),
	)

	_, err = engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "feishu:wiki:space-1:node-root",
		ParentKey: "",
		UseCache:  boolPtr(false),
		PageSize:  40,
	})
	if err != nil {
		t.Fatalf("list live root children: %v", err)
	}
	if len(spy.listRequests) != 1 || spy.listRequests[0].NodeRef != "wiki:space-1:node-root" {
		t.Fatalf("live root request should pass tree_key as provider node_ref, got %+v", spy.listRequests)
	}
}

func TestSourceTreeLiveWikiSpaceNodeRefPreserved(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{connectorType: connector.ConnectorType("feishu")}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:        "binding-1",
		SourceID:         "source-1",
		TreeKey:          "feishu:feishu:wiki:spaces",
		ConnectorType:    "feishu",
		TargetType:       "wiki_node",
		TargetRef:        "feishu:wiki:spaces",
		AuthConnectionID: "auth-1",
		Status:           "ACTIVE",
	}}
	engine := NewDBSourceTreeQueryEngine(
		repo,
		TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 100, MaxAllCurrentLevelItems: 10},
		WithSourceTreeConnectorRegistry(registry),
	)

	_, err = engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "feishu:feishu:wiki:spaces",
		ParentKey: "feishu:wiki:space:space-1",
		UseCache:  boolPtr(false),
		PageSize:  40,
	})
	if err != nil {
		t.Fatalf("list live wiki space children: %v", err)
	}
	if len(spy.listRequests) != 1 || spy.listRequests[0].NodeRef != "feishu:wiki:space:space-1" {
		t.Fatalf("wiki space node_ref should be preserved for connector traversal, got %+v", spy.listRequests)
	}
}

func TestSourceTreeLiveWikiRootFallsBackToBindingTargetWhenEmpty(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{
		connectorType: connector.ConnectorType("feishu"),
		childrenSet:   true,
		children:      []connector.RawObject{},
		fetchPage: connector.RawObjectPage{Items: []connector.RawObject{
			rawTreeObject("feishu:wiki:space-1:node-root", "", "Root Wiki", true, false),
		}},
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:         "binding-1",
		SourceID:          "source-1",
		TreeKey:           "feishu:wiki:space-1:node-root",
		ConnectorType:     "feishu",
		TargetType:        "wiki_node",
		TargetRef:         "wiki:space-1:node-root",
		BindingGeneration: 1,
		AuthConnectionID:  "auth-1",
		Status:            "ACTIVE",
	}}
	engine := NewDBSourceTreeQueryEngine(
		repo,
		TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 100, MaxAllCurrentLevelItems: 10},
		WithSourceTreeConnectorRegistry(registry),
	)

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "feishu:wiki:space-1:node-root",
		UseCache:  boolPtr(false),
		PageSize:  40,
	})
	if err != nil {
		t.Fatalf("list live wiki root children: %v", err)
	}
	if len(spy.fetchRequests) != 1 {
		t.Fatalf("expected empty live root to fetch binding target, got %d fetches", len(spy.fetchRequests))
	}
	gotFetch := spy.fetchRequests[0]
	if gotFetch.ScopeType != connector.ScopeTypeWatchEvent || gotFetch.ScopeRef["target_ref"] != "wiki:space-1:node-root" {
		t.Fatalf("unexpected root fetch request: %+v", gotFetch)
	}
	if page.SearchMode != SearchModeConnector || len(page.Items) != 1 {
		t.Fatalf("unexpected fallback page: %+v", page)
	}
	if page.Items[0].ObjectKey != "feishu:wiki:space-1:node-root" || !page.Items[0].Selectable {
		t.Fatalf("binding target document was not returned as selectable root: %+v", page.Items[0])
	}
}

func TestSourceTreeLiveWikiRootPreservesBindingRootLayer(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{
		connectorType: connector.ConnectorType("feishu"),
		childrenSet:   true,
		children: []connector.RawObject{
			rawTreeObject("feishu:wiki:space-1:node-child", "feishu:wiki:space-1:node-root", "Child Wiki", true, false),
		},
		fetchPage: connector.RawObjectPage{Items: []connector.RawObject{
			rawTreeObject("feishu:wiki:space-1:node-root", "", "Root Wiki", true, true),
		}},
	}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:         "binding-1",
		SourceID:          "source-1",
		TreeKey:           "feishu:wiki:space-1:node-root",
		ConnectorType:     "feishu",
		TargetType:        "wiki_node",
		TargetRef:         "wiki:space-1:node-root",
		BindingGeneration: 1,
		AuthConnectionID:  "auth-1",
		Status:            "ACTIVE",
	}}
	engine := NewDBSourceTreeQueryEngine(
		repo,
		TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 100, MaxAllCurrentLevelItems: 10},
		WithSourceTreeConnectorRegistry(registry),
	)

	rootPage, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "feishu:wiki:space-1:node-root",
		UseCache:  boolPtr(false),
		PageSize:  40,
	})
	if err != nil {
		t.Fatalf("list live wiki root: %v", err)
	}
	if len(spy.fetchRequests) != 1 {
		t.Fatalf("expected root request to fetch binding target, got %d fetches", len(spy.fetchRequests))
	}
	if len(rootPage.Items) != 1 || rootPage.Items[0].ObjectKey != "feishu:wiki:space-1:node-root" {
		t.Fatalf("root request should return binding root layer, got %+v", rootPage.Items)
	}
	if !rootPage.Items[0].Selectable || !rootPage.Items[0].IsDocument || !rootPage.Items[0].HasChildren {
		t.Fatalf("dual-role wiki root should be selectable and expandable: %+v", rootPage.Items[0])
	}

	childPage, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "feishu:wiki:space-1:node-root",
		ParentKey: "feishu:wiki:space-1:node-root",
		UseCache:  boolPtr(false),
		PageSize:  40,
	})
	if err != nil {
		t.Fatalf("list live wiki root children: %v", err)
	}
	if len(spy.fetchRequests) != 1 {
		t.Fatalf("expanded root should list children without fetching root again, got %d fetches", len(spy.fetchRequests))
	}
	if len(childPage.Items) != 1 || childPage.Items[0].ObjectKey != "feishu:wiki:space-1:node-child" {
		t.Fatalf("expanded root should return real children, got %+v", childPage.Items)
	}
}

func TestSourceTreeListChildrenUsesIndexedRepoWhenUseCache(t *testing.T) {
	t.Parallel()

	spy := &treeConnectorSpy{}
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		TreeKey:                "tree-root",
		CoreParentDocumentName: "Binding Root",
		ConnectorType:          string(treeTestConnectorType),
		TargetType:             string(treeTestTargetType),
		TargetRef:              "tree-test://root",
		Status:                 "ACTIVE",
	}}
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-1", "tree-root", "folder-1", "", "Guides", false, true),
		indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Welcome.md", true, false),
		indexedObject("source-1", "binding-1", "tree-root", "nested-1", "folder-1", "Nested.md", true, false),
	}
	engine := NewDBSourceTreeQueryEngine(
		repo,
		TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10},
		WithSourceTreeConnectorRegistry(registry),
	)

	roots, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{SourceID: "source-1"})
	if err != nil {
		t.Fatalf("list binding roots: %v", err)
	}
	if len(roots.Items) != 1 || roots.Items[0].BindingID != "binding-1" {
		t.Fatalf("unexpected binding roots: %+v", roots)
	}
	if !roots.Items[0].Selectable || !roots.Items[0].IsDocument || !roots.Items[0].IsContainer {
		t.Fatalf("binding root should be selectable as a sync target: %+v", roots.Items[0])
	}

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "tree-root",
		UseCache:  boolPtr(true),
		ParentKey: "",
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed children: %v", err)
	}
	if repo.getSourceCalls != 2 || repo.getBindingCalls != 1 || repo.listObjectsCalls != 2 {
		t.Fatalf("unexpected repo calls: source=%d binding=%d list=%d", repo.getSourceCalls, repo.getBindingCalls, repo.listObjectsCalls)
	}
	if len(spy.listRequests) != 0 {
		t.Fatalf("cached source tree should not access connector: %+v", spy.listRequests)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected current-level indexed children only, got %+v", page.Items)
	}
	for _, node := range page.Items {
		if node.ObjectKey == "nested-1" {
			t.Fatalf("source tree should not recursively build child levels: %+v", page.Items)
		}
	}
}

func TestSourceTreeListChildrenFiltersUnsupportedDocuments(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:         "binding-1",
		SourceID:          "source-1",
		TreeKey:           "tree-root",
		IncludeExtensions: store.JSON{"items": []any{"pdf"}},
		Status:            "ACTIVE",
	}}
	pdf := indexedObject("source-1", "binding-1", "tree-root", "pdf-1", "", "Guide.pdf", true, false)
	pdf.Object.FileExtension = ".pdf"
	script := indexedObject("source-1", "binding-1", "tree-root", "script-1", "", "script.py", true, false)
	script.Object.FileExtension = ".py"
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-1", "tree-root", "folder-1", "", "Folder", false, true),
		pdf,
		script,
	}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "tree-root",
		ParentKey: "",
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed children: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected folder and supported pdf only, got %+v", page.Items)
	}
	for _, item := range page.Items {
		if item.ObjectKey == "script-1" {
			t.Fatalf("unsupported file should not be returned: %+v", page.Items)
		}
	}
}

func TestSourceTreeListChildrenShowsOutOfScopeDocuments(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:         "binding-1",
		SourceID:          "source-1",
		TreeKey:           "tree-root",
		IncludeExtensions: store.JSON{"items": []any{"pdf"}},
		Status:            "ACTIVE",
	}}
	script := indexedObject("source-1", "binding-1", "tree-root", "script-1", "", "script.py", true, false)
	script.Object.FileExtension = ".py"
	script.State.SourceState = "OUT_OF_SCOPE"
	script.State.PendingAction = "DELETE"
	script.State.Selectable = true
	repo.objects = []ObjectWithState{script}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "tree-root",
		ParentKey: "",
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed children: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "script-1" {
		t.Fatalf("expected out-of-scope document to remain visible, got %+v", page.Items)
	}
	node := page.Items[0]
	if node.UpdateType != "cleanup" || node.UpdateDesc != "待清理" {
		t.Fatalf("out-of-scope document should render as cleanup: %+v", node)
	}
}

func TestSourceTreeListChildrenUsesSourceIncludeExtensions(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", IncludeExtensions: store.JSON{"items": []any{"pdf"}}}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID: "binding-1",
		SourceID:  "source-1",
		TreeKey:   "tree-root",
		Status:    "ACTIVE",
	}}
	pdf := indexedObject("source-1", "binding-1", "tree-root", "pdf-1", "", "Guide.pdf", true, false)
	pdf.Object.FileExtension = ".pdf"
	script := indexedObject("source-1", "binding-1", "tree-root", "script-1", "", "script.py", true, false)
	script.Object.FileExtension = ".py"
	repo.objects = []ObjectWithState{pdf, script}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "tree-root",
		ParentKey: "",
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed children: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "pdf-1" {
		t.Fatalf("expected only source-supported pdf, got %+v", page.Items)
	}
}

func TestSourceTreeBindingRootRequestReturnsAllBindingRootsForMultiBindingSource(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{
		{
			BindingID:              "binding-1",
			SourceID:               "source-1",
			TreeKey:                "wiki-root-1",
			CoreParentDocumentName: "test1",
			ConnectorType:          "feishu",
			TargetType:             "wiki_node",
			TargetRef:              "wiki:space-1:node-1",
			Status:                 "ACTIVE",
		},
		{
			BindingID:              "binding-2",
			SourceID:               "source-1",
			TreeKey:                "wiki-root-2",
			CoreParentDocumentName: "test1",
			ConnectorType:          "feishu",
			TargetType:             "wiki_node",
			TargetRef:              "wiki:space-1:node-2",
			Status:                 "ACTIVE",
		},
	}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "wiki-root-1",
		UseCache:  boolPtr(true),
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list binding roots: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected both same-name binding roots, got %+v", page.Items)
	}
	if page.Items[0].BindingID != "binding-1" || page.Items[1].BindingID != "binding-2" {
		t.Fatalf("unexpected binding roots: %+v", page.Items)
	}
	if page.Items[0].DisplayName != "test1" || page.Items[1].DisplayName != "test1" {
		t.Fatalf("same-name roots should preserve display names: %+v", page.Items)
	}
}

func TestSourceTreeBindingRootsUseIndexedRootDisplayNames(t *testing.T) {
	t.Parallel()

	base := newTreeReadRepo()
	base.sources["source-1"] = store.Source{SourceID: "source-1"}
	base.bindings["source-1"] = []store.Binding{
		{
			BindingID:              "binding-1",
			SourceID:               "source-1",
			TreeKey:                "wiki-root-1",
			CoreParentDocumentName: "source name",
			ConnectorType:          "feishu",
			TargetType:             "wiki_node",
			TargetRef:              "wiki:space-1:node-1",
			Status:                 "ACTIVE",
		},
		{
			BindingID:              "binding-2",
			SourceID:               "source-1",
			TreeKey:                "wiki-root-2",
			CoreParentDocumentName: "source name",
			ConnectorType:          "feishu",
			TargetType:             "wiki_node",
			TargetRef:              "wiki:space-1:node-2",
			Status:                 "ACTIVE",
		},
	}
	base.objects = []ObjectWithState{
		indexedObject("source-1", "binding-1", "wiki-root-1", "wiki-root-1", "", "三体1.pdf", true, false),
		indexedObject("source-1", "binding-2", "wiki-root-2", "wiki-root-2", "", "ADBE_2009_page_98.pdf", true, false),
	}
	repo := &treeReadRepoWithObject{treeReadRepo: base}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "wiki-root-1",
		UseCache:  boolPtr(true),
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list binding roots: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected both binding roots, got %+v", page.Items)
	}
	if page.Items[0].DisplayName != "三体1.pdf" || page.Items[1].DisplayName != "ADBE_2009_page_98.pdf" {
		t.Fatalf("binding roots should use indexed root display names: %+v", page.Items)
	}
	if page.Items[0].Key != "binding-1" || page.Items[0].ObjectKey != "wiki-root-1" {
		t.Fatalf("binding root identity should stay compatible: %+v", page.Items[0])
	}
}

func TestSourceTreeParentKeyCanSelectSiblingBindingRoot(t *testing.T) {
	t.Parallel()

	base := newTreeReadRepo()
	repo := &treeReadRepoWithObject{treeReadRepo: base}
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{
		{
			BindingID:              "binding-1",
			SourceID:               "source-1",
			TreeKey:                "wiki-root-1",
			CoreParentDocumentName: "test1",
			ConnectorType:          "feishu",
			TargetType:             "wiki_node",
			TargetRef:              "wiki:space-1:node-1",
			Status:                 "ACTIVE",
		},
		{
			BindingID:              "binding-2",
			SourceID:               "source-1",
			TreeKey:                "wiki-root-2",
			CoreParentDocumentName: "test1",
			ConnectorType:          "feishu",
			TargetType:             "wiki_node",
			TargetRef:              "wiki:space-1:node-2",
			Status:                 "ACTIVE",
		},
	}
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-2", "wiki-root-2", "wiki-root-2", "", "test1", true, true),
		indexedObject("source-1", "binding-2", "wiki-root-2", "doc-2", "wiki-root-2", "Nested.md", true, false),
	}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "wiki-root-1",
		UseCache:  boolPtr(true),
		ParentKey: "wiki-root-2",
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list sibling binding root children: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].BindingID != "binding-2" || page.Items[0].ObjectKey != "doc-2" {
		t.Fatalf("expected children from sibling binding root, got %+v", page.Items)
	}
}

func TestSourceTreeCachedChildrenExposeDocumentUpdateState(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID: "binding-1",
		SourceID:  "source-1",
		TreeKey:   "tree-root",
		Status:    "ACTIVE",
	}}
	deleted := indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Removed.md", true, false)
	deleted.State.SourceState = "DELETED"
	deleted.State.PendingAction = "DELETE"
	deleted.State.ParseQueueState = "PENDING"
	repo.objects = []ObjectWithState{deleted}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "tree-root",
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed children: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected deleted indexed document, got %+v", page.Items)
	}
	node := page.Items[0]
	if node.SourceState != "DELETED" || node.PendingAction != "DELETE" || node.ParseQueueState != "PENDING" {
		t.Fatalf("tree node did not expose document state: %+v", node)
	}
	if !node.HasUpdate || node.UpdateType != "deleted" || node.UpdateDesc != "源端删除待清理" {
		t.Fatalf("tree node did not expose document update status: %+v", node)
	}
}

func TestSourceTreeIndexedBindingRootDocumentExposesUpdateState(t *testing.T) {
	t.Parallel()

	base := newTreeReadRepo()
	base.sources["source-1"] = store.Source{SourceID: "source-1"}
	base.bindings["source-1"] = []store.Binding{{
		BindingID: "binding-1",
		SourceID:  "source-1",
		TreeKey:   "wiki-root",
		Status:    "ACTIVE",
	}}
	root := indexedObject("source-1", "binding-1", "wiki-root", "wiki-root", "", "Wiki Root", true, true)
	root.State.SourceState = "UNCHANGED"
	root.State.PendingAction = ""
	base.objects = []ObjectWithState{root}
	repo := &treeReadRepoWithObject{treeReadRepo: base}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		UseCache:  boolPtr(true),
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed binding root: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected binding root document, got %+v", page.Items)
	}
	node := page.Items[0]
	if node.ObjectKey != "wiki-root" || !node.IsDocument || !node.IsContainer {
		t.Fatalf("binding root should stay a dual-role wiki document: %+v", node)
	}
	if node.SourceState != "UNCHANGED" || node.UpdateType != "unchanged" || node.UpdateDesc != "当前文件已是最新" {
		t.Fatalf("binding root document state was not exposed: %+v", node)
	}
}

func TestSourceTreeIndexedBindingRootUsesVisibleChildrenForExpandState(t *testing.T) {
	t.Parallel()

	base := newTreeReadRepo()
	base.sources["source-1"] = store.Source{SourceID: "source-1"}
	base.bindings["source-1"] = []store.Binding{{
		BindingID:     "binding-1",
		SourceID:      "source-1",
		TreeKey:       "wiki-root",
		ConnectorType: "feishu",
		Status:        "ACTIVE",
	}}
	root := indexedObject("source-1", "binding-1", "wiki-root", "wiki-root", "", "Wiki Root", true, false)
	root.Object.HasChildren = false
	root.State.SourceState = "UNCHANGED"
	deletedChild := indexedObject("source-1", "binding-1", "wiki-root", "doc-deleted", "wiki-root", "Deleted.md", true, false)
	deletedChild.State.SourceState = "DELETED"
	deletedChild.State.PendingAction = "DELETE"
	deletedChild.State.DocumentListVisible = true
	base.objects = []ObjectWithState{root, deletedChild}
	repo := &treeReadRepoWithObject{treeReadRepo: base}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		UseCache:  boolPtr(true),
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed binding root: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected binding root document, got %+v", page.Items)
	}
	if !page.Items[0].HasChildren {
		t.Fatalf("binding root should stay expandable while it has visible deleted children: %+v", page.Items[0])
	}
}

func TestSourceTreeBindingRequestExpandsIndexedRootObject(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID: "binding-1",
		SourceID:  "source-1",
		TreeKey:   "tree-root",
		Status:    "ACTIVE",
	}}
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-1", "tree-root", "tree-root", "", "Binding Root", false, true),
		indexedObject("source-1", "binding-1", "tree-root", "folder-1", "tree-root", "Guides", false, true),
		indexedObject("source-1", "binding-1", "tree-root", "doc-1", "tree-root", "Welcome.md", true, false),
	}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		UseCache:  boolPtr(true),
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list indexed root children: %v", err)
	}
	if repo.listObjectsCalls != 1 || repo.lastList.ParentKey != "tree-root" {
		t.Fatalf("expected binding root expansion through tree key, calls=%d last=%+v", repo.listObjectsCalls, repo.lastList)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected root children, got %+v", page.Items)
	}
	for _, node := range page.Items {
		if node.ObjectKey == "tree-root" {
			t.Fatalf("binding request should expand root object instead of returning it: %+v", page.Items)
		}
		if node.ObjectKey == "folder-1" && (!node.Selectable || !node.IsDocument || !node.IsContainer) {
			t.Fatalf("container child should be selectable as a sync target: %+v", node)
		}
	}
}

func TestSourceTreeBindingRequestFallsBackToLegacyRootLevelObjects(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID: "binding-1",
		SourceID:  "source-1",
		TreeKey:   "tree-root",
		Status:    "ACTIVE",
	}}
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Welcome.md", true, false),
	}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		UseCache:  boolPtr(true),
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list legacy indexed root children: %v", err)
	}
	if repo.listObjectsCalls != 2 || repo.lastList.ParentKey != "" {
		t.Fatalf("expected fallback to legacy empty parent key, calls=%d last=%+v", repo.listObjectsCalls, repo.lastList)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "doc-1" {
		t.Fatalf("expected legacy root-level document, got %+v", page.Items)
	}
}

func TestSourceTreeAcceptsNodeRefAndTreeNodeKeyAsParent(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:     "binding-1",
		SourceID:      "source-1",
		TreeKey:       "tree-root",
		ConnectorType: "feishu",
		Status:        "ACTIVE",
	}}
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-1", "tree-root", "folder-1", "tree-root", "Guides", false, true),
		indexedObject("source-1", "binding-1", "tree-root", "nested-1", "folder-1", "Nested.md", true, false),
	}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		UseCache:  boolPtr(true),
		NodeRef:   "binding-1:folder-1",
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("list node_ref children: %v", err)
	}
	if repo.lastList.ParentKey != "folder-1" {
		t.Fatalf("expected source tree key to normalize to object key, got %+v", repo.lastList)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectKey != "nested-1" {
		t.Fatalf("expected nested child, got %+v", page.Items)
	}
}

func TestSourceTreeAllCurrentLevelRejectsTooManyIndexedChildren(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{BindingID: "binding-1", SourceID: "source-1", TreeKey: "tree-root"}}
	repo.objects = []ObjectWithState{
		indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "One.md", true, false),
		indexedObject("source-1", "binding-1", "tree-root", "doc-2", "", "Two.md", true, false),
	}
	engine := NewDBSourceTreeQueryEngine(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10, MaxAllCurrentLevelItems: 10})

	_, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		UseCache:  boolPtr(true),
		ListMode:  ListModeAllCurrentLevel,
		MaxItems:  1,
	})
	assertTreeErrorCode(t, err, ErrCodeResultTooLarge)
}

func TestSourceDocumentQueryReadsIndexedDocumentsOnly(t *testing.T) {
	t.Parallel()

	sourceModifiedAt := time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC)
	parsedAt := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{BindingID: "binding-1", SourceID: "source-1"}}
	object := indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Welcome", true, false).Object
	object.SizeBytes = 42
	object.FileExtension = ".md"
	object.ModifiedAt = &sourceModifiedAt
	repo.documents = []DocumentWithState{{
		Object: object,
		State:  store.DocumentState{SourceID: "source-1", BindingID: "binding-1", ObjectKey: "doc-1", SourceState: "NEW", SyncState: "IDLE", Selectable: true, SourceVersion: "v1", LastSyncedAt: &parsedAt},
		Document: &store.Document{
			DocumentID:     "document-1",
			SourceID:       "source-1",
			BindingID:      "binding-1",
			ObjectKey:      "doc-1",
			CoreDocumentID: "core-doc-1",
			ParseStatus:    "PENDING",
		},
	}}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})

	resp, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if repo.listDocumentsCalls != 1 || len(resp.Items) != 1 {
		t.Fatalf("expected indexed document query, calls=%d resp=%+v", repo.listDocumentsCalls, resp)
	}
	if resp.Items[0].CoreDocumentID != "core-doc-1" || resp.Items[0].SourceState != "NEW" {
		t.Fatalf("document state was not joined from indexed state: %+v", resp.Items[0])
	}
	if resp.Items[0].Name != "Welcome.md" || resp.Items[0].FileType != "md" || resp.Items[0].SizeBytes != 42 {
		t.Fatalf("document metadata was not mapped for datasource UI: %+v", resp.Items[0])
	}
	if resp.Items[0].DisplayName != "Welcome" {
		t.Fatalf("display_name should preserve source name: %+v", resp.Items[0])
	}
	if resp.Items[0].ModifiedAt != nil {
		t.Fatalf("modified_at should not carry source edit time for datasource UI: %+v", resp.Items[0])
	}
	if resp.Items[0].LastSyncedAt == nil || !resp.Items[0].LastSyncedAt.Equal(parsedAt) {
		t.Fatalf("last_synced_at should carry document parse time: %+v", resp.Items[0])
	}
	if resp.Items[0].SourceModifiedAt == nil || !resp.Items[0].SourceModifiedAt.Equal(sourceModifiedAt) {
		t.Fatalf("source_modified_at should carry source document edit time: %+v", resp.Items[0])
	}
	if resp.Summary["storage_bytes"] != int64(42) {
		t.Fatalf("document summary storage_bytes was not mapped: %+v", resp.Summary)
	}
}

func TestSourceDocumentQueryFiltersUnsupportedDocuments(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:         "binding-1",
		SourceID:          "source-1",
		IncludeExtensions: store.JSON{"items": []any{"pdf"}},
	}}
	pdf := indexedObject("source-1", "binding-1", "tree-root", "pdf-1", "", "Guide.pdf", true, false).Object
	pdf.FileExtension = ".pdf"
	script := indexedObject("source-1", "binding-1", "tree-root", "script-1", "", "script.py", true, false).Object
	script.FileExtension = ".py"
	repo.documents = []DocumentWithState{
		{Object: pdf, State: store.DocumentState{SourceID: "source-1", BindingID: "binding-1", ObjectKey: "pdf-1", SourceState: "NEW", SyncState: "IDLE", Selectable: true, SourceVersion: "v1", CreatedAt: now, UpdatedAt: now}},
		{Object: script, State: store.DocumentState{SourceID: "source-1", BindingID: "binding-1", ObjectKey: "script-1", SourceState: "NEW", SyncState: "IDLE", Selectable: true, SourceVersion: "v1", CreatedAt: now, UpdatedAt: now}},
	}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})

	resp, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ObjectKey != "pdf-1" {
		t.Fatalf("expected only supported pdf document, got %+v", resp.Items)
	}
}

func TestSourceDocumentQueryShowsOutOfScopeDocuments(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:         "binding-1",
		SourceID:          "source-1",
		IncludeExtensions: store.JSON{"items": []any{"pdf"}},
	}}
	script := indexedObject("source-1", "binding-1", "tree-root", "script-1", "", "script.py", true, false).Object
	script.FileExtension = ".py"
	repo.documents = []DocumentWithState{{
		Object: script,
		State: store.DocumentState{
			SourceID:            "source-1",
			BindingID:           "binding-1",
			ObjectKey:           "script-1",
			SourceState:         "OUT_OF_SCOPE",
			SyncState:           "IDLE",
			PendingAction:       "DELETE",
			DocumentListVisible: true,
			Selectable:          true,
			SourceVersion:       "v1",
			BaselineVersion:     "v1",
			CreatedAt:           now,
			UpdatedAt:           now,
		},
	}}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})

	resp, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ObjectKey != "script-1" {
		t.Fatalf("expected out-of-scope document, got %+v", resp.Items)
	}
	if resp.Items[0].UpdateType != "cleanup" || resp.Items[0].UpdateDesc != "待清理" {
		t.Fatalf("out-of-scope document should render as cleanup: %+v", resp.Items[0])
	}
}

func TestSourceDocumentQueryMarksUnparsedUpdatesPendingParse(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{BindingID: "binding-1", SourceID: "source-1", ConnectorType: "feishu"}}
	object := indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Welcome", true, false).Object
	repo.documents = []DocumentWithState{{
		Object: object,
		State: store.DocumentState{
			SourceID:        "source-1",
			BindingID:       "binding-1",
			ObjectKey:       "doc-1",
			SourceState:     "NEW",
			SyncState:       "IDLE",
			PendingAction:   "CREATE",
			ParseQueueState: "NONE",
			Selectable:      true,
		},
	}}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})

	resp, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected one document, got %+v", resp.Items)
	}
	if resp.Items[0].ParseQueueState != "PENDING_PARSE" || resp.Items[0].ParseState != "PENDING_PARSE" {
		t.Fatalf("unparsed update should be marked pending parse: %+v", resp.Items[0])
	}
	if resp.Items[0].EffectiveParseStatus != parseStatePendingParse {
		t.Fatalf("unparsed update should not be marked downloading: %+v", resp.Items[0])
	}
}

func TestSourceDocumentQueryKeepsActiveQueueStateForExistingDocument(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{BindingID: "binding-1", SourceID: "source-1"}}
	object := indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Welcome", true, false).Object
	repo.documents = []DocumentWithState{{
		Object: object,
		State: store.DocumentState{
			SourceID:            "source-1",
			BindingID:           "binding-1",
			ObjectKey:           "doc-1",
			SourceState:         "MODIFIED",
			SyncState:           "IDLE",
			PendingAction:       "REPARSE",
			DocumentListVisible: true,
			Selectable:          true,
			ParseQueueState:     "RUNNING",
		},
		Document: &store.Document{
			DocumentID:  "document-1",
			SourceID:    "source-1",
			BindingID:   "binding-1",
			ObjectKey:   "doc-1",
			ParseStatus: "SUCCEEDED",
		},
	}}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})

	resp, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ParseStatus != "SUCCEEDED" || resp.Items[0].ParseState != "RUNNING" {
		t.Fatalf("active queue state should not be hidden by previous document status: %+v", resp.Items)
	}
}

func TestSourceDocumentQueryComputesEffectiveParseStatus(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{
		{BindingID: "binding-cloud", SourceID: "source-1", ConnectorType: "feishu"},
		{BindingID: "binding-local", SourceID: "source-1", ConnectorType: "local_fs"},
	}
	cloudRunning := indexedObject("source-1", "binding-cloud", "tree-root", "cloud-running", "", "Cloud Running.md", true, false).Object
	cloudFailed := indexedObject("source-1", "binding-cloud", "tree-root", "cloud-failed", "", "Cloud Failed.md", true, false).Object
	cloudCanceled := indexedObject("source-1", "binding-cloud", "tree-root", "cloud-canceled", "", "Cloud Canceled.md", true, false).Object
	localRunning := indexedObject("source-1", "binding-local", "tree-root", "local-running", "", "Local Running.md", true, false).Object
	localFailed := indexedObject("source-1", "binding-local", "tree-root", "local-failed", "", "Local Failed.md", true, false).Object
	repo.documents = []DocumentWithState{
		{
			Object: cloudRunning,
			State: store.DocumentState{
				SourceID:        "source-1",
				BindingID:       "binding-cloud",
				ObjectKey:       "cloud-running",
				SourceState:     "NEW",
				SyncState:       "IDLE",
				ParseQueueState: store.ParseTaskStatusRunning,
			},
			Document: &store.Document{DocumentID: "document-cloud-running", SourceID: "source-1", BindingID: "binding-cloud", ObjectKey: "cloud-running", ParseStatus: store.ParseTaskStatusPending},
		},
		{
			Object: cloudFailed,
			State: store.DocumentState{
				SourceID:        "source-1",
				BindingID:       "binding-cloud",
				ObjectKey:       "cloud-failed",
				SourceState:     "NEW",
				SyncState:       "IDLE",
				ParseQueueState: store.ParseTaskStatusFailed,
				LastError:       store.JSON{"reason": "PERMISSION_DENIED"},
			},
			Document: &store.Document{DocumentID: "document-cloud-failed", SourceID: "source-1", BindingID: "binding-cloud", ObjectKey: "cloud-failed", ParseStatus: store.ParseTaskStatusFailed},
		},
		{
			Object: cloudCanceled,
			State: store.DocumentState{
				SourceID:        "source-1",
				BindingID:       "binding-cloud",
				ObjectKey:       "cloud-canceled",
				SourceState:     "NEW",
				SyncState:       "IDLE",
				ParseQueueState: store.ParseTaskStatusFailed,
				LastError:       store.JSON{"code": "CORE_TASK_FAILED", "phase": "parse"},
			},
			Document: &store.Document{DocumentID: "document-cloud-canceled", SourceID: "source-1", BindingID: "binding-cloud", ObjectKey: "cloud-canceled", ParseStatus: "CANCELED"},
		},
		{
			Object: localRunning,
			State: store.DocumentState{
				SourceID:        "source-1",
				BindingID:       "binding-local",
				ObjectKey:       "local-running",
				SourceState:     "NEW",
				SyncState:       "IDLE",
				ParseQueueState: store.ParseTaskStatusRunning,
			},
			Document: &store.Document{DocumentID: "document-local-running", SourceID: "source-1", BindingID: "binding-local", ObjectKey: "local-running", ParseStatus: store.ParseTaskStatusPending},
		},
		{
			Object: localFailed,
			State: store.DocumentState{
				SourceID:        "source-1",
				BindingID:       "binding-local",
				ObjectKey:       "local-failed",
				SourceState:     "NEW",
				SyncState:       "IDLE",
				ParseQueueState: store.ParseTaskStatusFailed,
				LastError:       store.JSON{"reason": "PERMISSION_DENIED"},
			},
			Document: &store.Document{DocumentID: "document-local-failed", SourceID: "source-1", BindingID: "binding-local", ObjectKey: "local-failed", ParseStatus: store.ParseTaskStatusFailed},
		},
	}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})

	resp, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1"})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	statuses := map[string]string{}
	parseStates := map[string]string{}
	for _, item := range resp.Items {
		statuses[item.ObjectKey] = item.EffectiveParseStatus
		parseStates[item.ObjectKey] = item.ParseState
	}
	if statuses["cloud-running"] != effectiveParseStatusDownloading {
		t.Fatalf("cloud running task should be downloading, got statuses=%+v", statuses)
	}
	if statuses["cloud-failed"] != effectiveParseStatusDownloadFailed {
		t.Fatalf("cloud permission failure should be download_failed, got statuses=%+v", statuses)
	}
	if statuses["cloud-canceled"] != effectiveParseStatusCanceled || parseStates["cloud-canceled"] != effectiveParseStatusCanceled {
		t.Fatalf("cloud canceled task should expose canceled state, got statuses=%+v parseStates=%+v", statuses, parseStates)
	}
	if statuses["local-running"] != effectiveParseStatusParsing {
		t.Fatalf("local running task should stay parsing, got statuses=%+v", statuses)
	}
	if statuses["local-failed"] != effectiveParseStatusFailed {
		t.Fatalf("local permission failure should stay failed, got statuses=%+v", statuses)
	}
}

func TestSourceDocumentQueryDedupesSamePathAndPrefersActiveParse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{BindingID: "binding-1", SourceID: "source-1"}}
	parsed := indexedObject("source-1", "binding-1", "tree-root", "doc-old", "folder-1", "CN-43", true, false).Object
	parsed.FileExtension = ".txt"
	pending := indexedObject("source-1", "binding-1", "tree-root", "doc-new", "folder-1", "CN-43", true, false).Object
	pending.FileExtension = ".txt"
	repo.documents = []DocumentWithState{
		{
			Object: parsed,
			State: store.DocumentState{
				SourceID:            "source-1",
				BindingID:           "binding-1",
				ObjectKey:           "doc-old",
				SourceState:         "UNCHANGED",
				SyncState:           "IDLE",
				DocumentListVisible: true,
				Selectable:          true,
				ParseQueueState:     "NONE",
				LastSyncedAt:        &now,
			},
			Document: &store.Document{
				DocumentID:  "document-old",
				SourceID:    "source-1",
				BindingID:   "binding-1",
				ObjectKey:   "doc-old",
				DisplayName: "CN-43",
				ParseStatus: "SUCCEEDED",
			},
		},
		{
			Object: pending,
			State: store.DocumentState{
				SourceID:            "source-1",
				BindingID:           "binding-1",
				ObjectKey:           "doc-new",
				SourceState:         "NEW",
				SyncState:           "IDLE",
				PendingAction:       "CREATE",
				DocumentListVisible: true,
				Selectable:          true,
				ParseQueueState:     "PENDING",
			},
			Document: &store.Document{
				DocumentID:  "document-new",
				SourceID:    "source-1",
				BindingID:   "binding-1",
				ObjectKey:   "doc-new",
				DisplayName: "CN-43",
				ParseStatus: "PENDING",
			},
		},
	}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 10})

	resp, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("same-path documents should be collapsed, got total=%d items=%+v", resp.Total, resp.Items)
	}
	if resp.Items[0].ObjectKey != "doc-new" || resp.Items[0].ParseState != "PENDING" {
		t.Fatalf("active parse row should win over stale parsed row: %+v", resp.Items[0])
	}
}

func TestSourceDocumentQueryPassesStateParseFiltersAndPagination(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{BindingID: "binding-1", SourceID: "source-1"}}
	query := NewDBSourceDocumentQuery(repo, TreeQueryLimits{DefaultPageSize: 10, MaxPageSize: 100})

	_, err := query.ListDocuments(context.Background(), SourceDocumentListRequest{
		SourceID:      "source-1",
		BindingID:     "binding-1",
		StateFilter:   []string{"NEW"},
		ParseStatuses: []string{"PENDING"},
		Page:          3,
		PageSize:      50,
	})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if got := repo.lastDocumentList; got.Page != 3 || got.PageSize != 50 || len(got.StateFilter) != 1 || got.StateFilter[0] != "NEW" || len(got.ParseStatuses) != 1 || got.ParseStatuses[0] != "PENDING" {
		t.Fatalf("document filters were not passed to store: %+v", got)
	}
}

func assertTreeErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	if got := ErrorCodeOf(err); got != code {
		t.Fatalf("expected error code %s, got %s (%v)", code, got, err)
	}
}

const (
	treeTestConnectorType connector.ConnectorType = "tree_test"
	treeTestTargetType    connector.TargetType    = "tree_test_root"
)

type treeConnectorSpy struct {
	connectorType     connector.ConnectorType
	supportsSearch    bool
	childrenSet       bool
	children          []connector.RawObject
	childrenByNodeRef map[string][]connector.RawObject
	listErr           error
	repeatCursor      bool
	fetchPage         connector.RawObjectPage
	listRequests      []connector.ListChildrenRequest
	fetchRequests     []connector.FetchPageRequest
	searchRequests    []connector.SearchRequest
	mapObjects        []connector.RawObject
}

func (c *treeConnectorSpy) Spec() connector.ConnectorSpec {
	connectorType := c.connectorType
	if connectorType == "" {
		connectorType = treeTestConnectorType
	}
	return connector.ConnectorSpec{
		ConnectorType:         connectorType,
		TargetTypes:           []connector.TargetType{treeTestTargetType},
		SupportsSearch:        c.supportsSearch,
		SupportsExportFormats: []connector.ExportFormat{connector.ExportFormatOriginal},
		MaxPageSize:           2,
	}
}

func (c *treeConnectorSpy) ValidateTarget(context.Context, connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	return connector.NormalizedTarget{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

func (c *treeConnectorSpy) ListChildren(_ context.Context, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	c.listRequests = append(c.listRequests, req)
	if c.listErr != nil {
		return connector.RawObjectPage{}, c.listErr
	}
	if c.repeatCursor {
		return connector.RawObjectPage{
			Items:      []connector.RawObject{rawTreeObject("doc-1", "", "Welcome.md", true, false)},
			HasMore:    true,
			NextCursor: "cursor-1",
		}, nil
	}
	items, hasNodeChildren := c.childrenForListRequest(req)
	if !hasNodeChildren && !c.childrenSet && len(items) == 0 {
		items = []connector.RawObject{
			rawTreeObject("folder-1", "", "Guides", false, true),
			rawTreeObject("doc-1", "", "Welcome.md", true, false),
			rawTreeObject("page-1", "", "Portal", true, true),
		}
	}
	offset := 0
	if req.Cursor == "2" {
		offset = 2
	}
	end := offset + req.PageSize
	if end > len(items) {
		end = len(items)
	}
	page := connector.RawObjectPage{Items: items[offset:end]}
	if end < len(items) {
		page.HasMore = true
		page.NextCursor = "2"
	}
	return page, nil
}

func (c *treeConnectorSpy) childrenForListRequest(req connector.ListChildrenRequest) ([]connector.RawObject, bool) {
	if c.childrenByNodeRef == nil {
		return c.children, false
	}
	key := strings.TrimSpace(req.NodeRef)
	if key == "" {
		key = strings.TrimSpace(req.TargetRef)
	}
	items, ok := c.childrenByNodeRef[key]
	return items, ok
}

func (c *treeConnectorSpy) Search(_ context.Context, req connector.SearchRequest) (connector.RawObjectPage, error) {
	c.searchRequests = append(c.searchRequests, req)
	return connector.RawObjectPage{Items: []connector.RawObject{rawTreeObject("doc-1", "", "Welcome.md", true, false)}}, nil
}

func (c *treeConnectorSpy) FetchPage(_ context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	c.fetchRequests = append(c.fetchRequests, req)
	if len(c.fetchPage.Items) > 0 || c.fetchPage.HasMore || c.fetchPage.NextCursor != "" || c.fetchPage.ListComplete {
		return c.fetchPage, nil
	}
	return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

func (c *treeConnectorSpy) ExportObject(context.Context, connector.ExportObjectRequest) (connector.ExportedObject, error) {
	return connector.ExportedObject{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

func (c *treeConnectorSpy) MapObject(_ context.Context, raw connector.RawObject) (connector.NormalizedSourceObject, error) {
	c.mapObjects = append(c.mapObjects, raw)
	return connector.NormalizedSourceObject{
		ObjectKey:     raw.ObjectKey,
		ParentKey:     raw.ParentKey,
		DisplayName:   raw.DisplayName,
		SearchName:    raw.SearchName,
		ObjectType:    raw.ObjectType,
		IsDocument:    raw.IsDocument,
		IsContainer:   raw.IsContainer,
		HasChildren:   raw.HasChildren,
		SourceVersion: raw.SourceVersion,
		ProviderMeta:  raw.ProviderMeta,
	}, nil
}

func rawTreeObject(objectKey, parentKey, displayName string, isDocument, isContainer bool) connector.RawObject {
	objectType := connector.ObjectTypeFile
	if isContainer {
		objectType = connector.ObjectTypeFolder
	}
	return connector.RawObject{
		ObjectRef:     objectKey,
		ObjectKey:     objectKey,
		ParentKey:     parentKey,
		DisplayName:   displayName,
		SearchName:    displayName,
		ObjectType:    objectType,
		IsDocument:    isDocument,
		IsContainer:   isContainer,
		HasChildren:   isContainer,
		SourceVersion: "v1",
		ProviderMeta:  connector.ProviderMeta{"id": objectKey},
	}
}

type panicFallbackSearch struct {
	t      *testing.T
	called bool
}

func (s *panicFallbackSearch) Search(context.Context, TargetTreeSearchRequest) (TreeNodePage, error) {
	s.called = true
	s.t.Fatalf("fallback search should not be called")
	return TreeNodePage{}, nil
}

type memoryTargetSearchCacheStore struct {
	snapshots map[string]targetSearchCacheSnapshot
	locks     int
	sets      int
	locked    bool
}

func newMemoryTargetSearchCacheStore() *memoryTargetSearchCacheStore {
	return &memoryTargetSearchCacheStore{snapshots: map[string]targetSearchCacheSnapshot{}}
}

func (s *memoryTargetSearchCacheStore) Get(_ context.Context, key string) (targetSearchCacheSnapshot, bool, error) {
	snapshot, ok := s.snapshots[key]
	if ok {
		snapshot.stale = !snapshot.staleAt.IsZero() && time.Now().After(snapshot.staleAt)
		return snapshot, true, nil
	}
	if s.locked {
		return targetSearchCacheSnapshot{status: targetSearchCacheStatusBuilding, building: true}, true, nil
	}
	return targetSearchCacheSnapshot{}, false, nil
}

func (s *memoryTargetSearchCacheStore) Set(_ context.Context, key string, snapshot targetSearchCacheSnapshot, staleTTL, _ time.Duration) error {
	s.sets++
	s.locked = false
	if snapshot.staleAt.IsZero() {
		snapshot.staleAt = time.Now().Add(staleTTL)
	}
	snapshot.stale = !snapshot.staleAt.IsZero() && time.Now().After(snapshot.staleAt)
	s.snapshots[key] = snapshot
	return nil
}

func (s *memoryTargetSearchCacheStore) TryLock(context.Context, string, time.Duration) (bool, error) {
	if s.locked {
		return false, nil
	}
	s.locks++
	s.locked = true
	return true, nil
}

type treeReadRepo struct {
	sources   map[string]store.Source
	bindings  map[string][]store.Binding
	objects   []ObjectWithState
	documents []DocumentWithState

	getSourceCalls     int
	listBindingsCalls  int
	getBindingCalls    int
	listObjectsCalls   int
	searchObjectsCalls int
	listDocumentsCalls int
	lastList           ObjectListRequest
	lastSearch         ObjectSearchRequest
	lastDocumentList   store.SourceDocumentListRequest
}

func newTreeReadRepo() *treeReadRepo {
	return &treeReadRepo{
		sources:  map[string]store.Source{},
		bindings: map[string][]store.Binding{},
	}
}

type treeReadRepoWithObject struct {
	*treeReadRepo
}

func (r *treeReadRepoWithObject) GetObject(_ context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error) {
	for _, item := range r.objects {
		if item.Object.SourceID == sourceID && item.Object.BindingID == bindingID && item.Object.ObjectKey == objectKey {
			return item.Object, nil
		}
	}
	return store.SourceObject{}, store.NewStoreError(store.ErrCodeNotFound, "object not found")
}

func (r *treeReadRepoWithObject) GetDocumentState(_ context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error) {
	for _, item := range r.objects {
		if item.Object.SourceID == sourceID && item.Object.BindingID == bindingID && item.Object.ObjectKey == objectKey && item.State != nil {
			return *item.State, nil
		}
	}
	return store.DocumentState{}, store.NewStoreError(store.ErrCodeNotFound, "document state not found")
}

func (r *treeReadRepo) GetSource(_ context.Context, sourceID string) (store.Source, error) {
	r.getSourceCalls++
	src, ok := r.sources[sourceID]
	if !ok {
		return store.Source{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return src, nil
}

func (r *treeReadRepo) ListBindings(_ context.Context, sourceID string) ([]store.Binding, error) {
	r.listBindingsCalls++
	if _, ok := r.sources[sourceID]; !ok {
		return nil, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return append([]store.Binding(nil), r.bindings[sourceID]...), nil
}

func (r *treeReadRepo) GetBinding(_ context.Context, sourceID, bindingID string) (store.Binding, error) {
	r.getBindingCalls++
	for _, binding := range r.bindings[sourceID] {
		if binding.BindingID == bindingID {
			return binding, nil
		}
	}
	return store.Binding{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
}

func (r *treeReadRepo) ListObjects(_ context.Context, req ObjectListRequest) ([]ObjectWithState, string, bool, error) {
	r.listObjectsCalls++
	r.lastList = req
	items := make([]ObjectWithState, 0)
	for _, item := range r.objects {
		if objectMatchesList(item.Object, req) {
			items = append(items, item)
		}
	}
	return paginateObjects(items, req.PageSize, req.Cursor)
}

func (r *treeReadRepo) SearchObjects(_ context.Context, req ObjectSearchRequest) ([]ObjectWithState, string, bool, error) {
	r.searchObjectsCalls++
	r.lastSearch = req
	items := make([]ObjectWithState, 0)
	for _, item := range r.objects {
		if req.SourceID != "" && item.Object.SourceID != req.SourceID {
			continue
		}
		if req.BindingID != "" && item.Object.BindingID != req.BindingID {
			continue
		}
		if req.TreeKey != "" && item.Object.TreeKey != req.TreeKey {
			continue
		}
		if req.Keyword != "" && item.Object.DisplayName != "Handbook.md" {
			continue
		}
		items = append(items, item)
	}
	return paginateObjects(items, req.PageSize, req.Cursor)
}

func (r *treeReadRepo) ListDocuments(_ context.Context, req store.SourceDocumentListRequest) ([]DocumentWithState, int, error) {
	r.listDocumentsCalls++
	r.lastDocumentList = req
	items := make([]DocumentWithState, 0)
	for _, item := range r.documents {
		if item.Object.SourceID == req.SourceID && (req.BindingID == "" || item.Object.BindingID == req.BindingID) {
			items = append(items, item)
		}
	}
	return items, len(items), nil
}

func (r *treeReadRepo) GetSourceSummary(_ context.Context, req store.SourceSummaryRequest) (store.SourceSummary, error) {
	summary := store.SourceSummary{SourceID: req.SourceID, BindingID: req.BindingID}
	for _, item := range r.documents {
		if item.Object.SourceID != req.SourceID || (req.BindingID != "" && item.Object.BindingID != req.BindingID) {
			continue
		}
		summary.DocumentObjects++
		summary.StorageBytes += item.Object.SizeBytes
		store.AddSourceStateCount(&summary, item.State.SourceState, 1)
	}
	return summary, nil
}

func objectMatchesList(object store.SourceObject, req ObjectListRequest) bool {
	if object.SourceID != req.SourceID || object.BindingID != req.BindingID || object.TreeKey != req.TreeKey || object.ParentKey != req.ParentKey {
		return false
	}
	if object.IsDocument && !req.IncludeDocuments {
		return false
	}
	if object.IsContainer && !req.IncludeContainers {
		return false
	}
	return true
}

func paginateObjects(items []ObjectWithState, pageSize int, cursor string) ([]ObjectWithState, string, bool, error) {
	offset := 0
	if cursor != "" {
		offset = 1
	}
	if offset >= len(items) {
		return nil, "", false, nil
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	nextCursor := ""
	hasMore := end < len(items)
	if hasMore {
		nextCursor = "next"
	}
	return append([]ObjectWithState(nil), items[offset:end]...), nextCursor, hasMore, nil
}

func boolPtr(value bool) *bool {
	return &value
}

func indexedObject(sourceID, bindingID, treeKey, objectKey, parentKey, displayName string, isDocument, isContainer bool) ObjectWithState {
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	state := store.DocumentState{
		SourceID:            sourceID,
		BindingID:           bindingID,
		ObjectKey:           objectKey,
		SourceState:         "NEW",
		SyncState:           "IDLE",
		DocumentListVisible: true,
		Selectable:          isDocument,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	return ObjectWithState{
		Object: store.SourceObject{
			SourceID:      sourceID,
			BindingID:     bindingID,
			TreeKey:       treeKey,
			ObjectKey:     objectKey,
			ParentKey:     parentKey,
			DisplayName:   displayName,
			SearchName:    displayName,
			ObjectType:    "file",
			IsDocument:    isDocument,
			IsContainer:   isContainer,
			HasChildren:   isContainer,
			SourceVersion: "v1",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		State: &state,
	}
}

var _ connector.SourceConnector = (*treeConnectorSpy)(nil)
var _ SourceTreeReadRepository = (*treeReadRepo)(nil)
