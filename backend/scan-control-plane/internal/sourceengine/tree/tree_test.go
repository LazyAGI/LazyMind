package tree

import (
	"context"
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
	if len(page.Items) != 2 {
		t.Fatalf("expected current page nodes only, got %+v", page.Items)
	}
	if page.Items[0].ObjectKey != "folder-1" || page.Items[1].ObjectKey != "doc-1" {
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
	if !page.ListComplete || page.HasMore || len(page.Items) != 3 {
		t.Fatalf("expected complete current-level page, got %+v", page)
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

func TestSourceTreeListChildrenUsesLiveConnectorByDefault(t *testing.T) {
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
		PageSize:  40,
	})
	if err != nil {
		t.Fatalf("list live root children: %v", err)
	}
	if len(spy.listRequests) != 1 || spy.listRequests[0].NodeRef != "wiki:space-1:node-root" {
		t.Fatalf("live root request should pass tree_key as provider node_ref, got %+v", spy.listRequests)
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

	page, err := engine.ListChildren(context.Background(), SourceTreeChildrenRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		TreeKey:   "tree-root",
		UseCache:  true,
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
		UseCache:  true,
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
		UseCache:  true,
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
		UseCache:  true,
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
		UseCache:  true,
		ListMode:  ListModeAllCurrentLevel,
		MaxItems:  1,
	})
	assertTreeErrorCode(t, err, ErrCodeResultTooLarge)
}

func TestSourceDocumentQueryReadsIndexedDocumentsOnly(t *testing.T) {
	t.Parallel()

	repo := newTreeReadRepo()
	repo.sources["source-1"] = store.Source{SourceID: "source-1"}
	repo.bindings["source-1"] = []store.Binding{{BindingID: "binding-1", SourceID: "source-1"}}
	object := indexedObject("source-1", "binding-1", "tree-root", "doc-1", "", "Welcome", true, false).Object
	object.SizeBytes = 42
	object.FileExtension = ".md"
	repo.documents = []DocumentWithState{{
		Object: object,
		State:  store.DocumentState{SourceID: "source-1", BindingID: "binding-1", ObjectKey: "doc-1", SourceState: "NEW", SyncState: "IDLE", Selectable: true, SourceVersion: "v1"},
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
	if resp.Summary["storage_bytes"] != int64(42) {
		t.Fatalf("document summary storage_bytes was not mapped: %+v", resp.Summary)
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
	connectorType  connector.ConnectorType
	supportsSearch bool
	childrenSet    bool
	children       []connector.RawObject
	fetchPage      connector.RawObjectPage
	listRequests   []connector.ListChildrenRequest
	fetchRequests  []connector.FetchPageRequest
	searchRequests []connector.SearchRequest
	mapObjects     []connector.RawObject
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
	items := c.children
	if !c.childrenSet && len(items) == 0 {
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
