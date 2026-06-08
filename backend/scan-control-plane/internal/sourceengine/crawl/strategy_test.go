package crawl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestFullStrategyUsesRecursiveFetchWhenSupported(t *testing.T) {
	t.Parallel()

	strategy := NewFullCrawlStrategy(strategyInput{
		binding:  strategyBinding(),
		spec:     connector.ConnectorSpec{SupportsRecursiveFetch: true},
		claim:    strategyClaim(connector.ScopeTypeFull, nil),
		pageSize: 25,
	})

	req, done, err := strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("next request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindFetch || req.Fetch.ScopeType != connector.ScopeTypeFull || req.Fetch.PageSize != 25 {
		t.Fatalf("unexpected full fetch request: %+v", req)
	}
	if err := strategy.ObservePage(context.Background(), connector.RawObjectPage{Items: []connector.RawObject{{ObjectKey: "doc-1"}}}); err != nil {
		t.Fatalf("observe page: %v", err)
	}
	coverage := strategy.Coverage()
	if !coverage.Complete || !coverage.CoveredTargetRoot || len(coverage.CoveredObjectKeys) != 1 || coverage.CoveredObjectKeys[0] != "doc-1" {
		t.Fatalf("unexpected full coverage: %+v", coverage)
	}
}

func TestFullStrategyUsesBFSListChildrenFallback(t *testing.T) {
	t.Parallel()

	strategy := NewFullCrawlStrategy(strategyInput{
		binding:  strategyBinding(),
		spec:     connector.ConnectorSpec{SupportsRecursiveFetch: false},
		claim:    strategyClaim(connector.ScopeTypeFull, nil),
		pageSize: 10,
	})

	req, done, err := strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("first next request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindListChildren || req.ListChildren.NodeRef != "" {
		t.Fatalf("expected root list request, got %+v", req)
	}
	err = strategy.ObservePage(context.Background(), connector.RawObjectPage{Items: []connector.RawObject{{ObjectKey: "folder-1", IsContainer: true}}})
	if err != nil {
		t.Fatalf("observe root page: %v", err)
	}
	req, done, err = strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("second next request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindListChildren || req.ListChildren.NodeRef != "folder-1" {
		t.Fatalf("expected child list request, got %+v", req)
	}
}

func TestFullStrategyFetchesDualRoleRootBeforeBFSChildren(t *testing.T) {
	t.Parallel()

	strategy := NewFullCrawlStrategy(strategyInput{
		binding: strategyBinding(),
		spec: connector.ConnectorSpec{
			SupportsRecursiveFetch: false,
			SupportsDualRoleObject: true,
		},
		claim:    strategyClaim(connector.ScopeTypeFull, nil),
		pageSize: 10,
	})

	req, done, err := strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("first next request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindFetch || req.Fetch.ScopeType != connector.ScopeTypeWatchEvent {
		t.Fatalf("expected dual-role root fetch, got %+v", req)
	}
	if err := strategy.ObservePage(context.Background(), connector.RawObjectPage{Items: []connector.RawObject{{ObjectKey: "root", IsDocument: true}}}); err != nil {
		t.Fatalf("observe root fetch: %v", err)
	}

	req, done, err = strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("second next request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindListChildren || req.ListChildren.NodeRef != "" {
		t.Fatalf("expected root children list after root fetch, got %+v", req)
	}
}

func TestPartialStrategyCoversDeclaredSubtreeOnly(t *testing.T) {
	t.Parallel()

	strategy := NewPartialCrawlStrategy(strategyInput{
		binding:  strategyBinding(),
		spec:     connector.ConnectorSpec{SupportsRecursiveFetch: false},
		claim:    strategyClaim(connector.ScopeTypePartial, connector.ScopeRef{"subtree_root": "folder-1"}),
		pageSize: 10,
	})
	req, done, err := strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("next request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindListChildren || req.ListChildren.NodeRef != "folder-1" {
		t.Fatalf("expected subtree list request, got %+v", req)
	}
	if err := strategy.ObservePage(context.Background(), connector.RawObjectPage{}); err != nil {
		t.Fatalf("observe partial page: %v", err)
	}
	coverage := strategy.Coverage()
	if !coverage.Complete || coverage.CoveredTargetRoot || len(coverage.CoveredSubtrees) != 1 || coverage.CoveredSubtrees[0] != "folder-1" {
		t.Fatalf("unexpected partial coverage: %+v", coverage)
	}
}

func TestPartialStrategyFetchesSelectedPath(t *testing.T) {
	t.Parallel()

	strategy := NewPartialCrawlStrategy(strategyInput{
		binding:  strategyBinding(),
		spec:     connector.ConnectorSpec{SupportsRecursiveFetch: false},
		claim:    strategyClaim(connector.ScopeTypePartial, connector.ScopeRef{"path": "/workspace/docs/111.txt"}),
		pageSize: 10,
	})
	req, done, err := strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("next request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindFetch || req.Fetch.ScopeRef["path"] != "/workspace/docs/111.txt" {
		t.Fatalf("expected selected path fetch request, got %+v", req)
	}
	if err := strategy.ObservePage(context.Background(), connector.RawObjectPage{Items: []connector.RawObject{{ObjectKey: "doc-1"}}}); err != nil {
		t.Fatalf("observe selected path page: %v", err)
	}
	coverage := strategy.Coverage()
	if !coverage.Complete || len(coverage.CoveredObjectKeys) != 1 || coverage.CoveredObjectKeys[0] != "doc-1" {
		t.Fatalf("unexpected selected path coverage: %+v", coverage)
	}
}

func TestDeltaStrategyRequestsCursorAndReportsUnsupported(t *testing.T) {
	t.Parallel()

	unsupported := NewDeltaCrawlStrategy(strategyInput{
		binding: strategyBinding(),
		spec:    connector.ConnectorSpec{SupportsDelta: false},
		claim:   strategyClaim(connector.ScopeTypeDelta, nil),
	})
	if _, _, err := unsupported.NextRequest(context.Background(), CrawlLoopState{}); !errors.Is(err, errDeltaUnsupported) {
		t.Fatalf("expected unsupported delta error, got %v", err)
	}

	strategy := NewDeltaCrawlStrategy(strategyInput{
		binding:  strategyBinding(),
		spec:     connector.ConnectorSpec{SupportsDelta: true},
		claim:    strategyClaim(connector.ScopeTypeDelta, nil),
		pageSize: 10,
	})
	strategy.cursor = "cursor-1"
	req, done, err := strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("next delta request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindFetch || req.Fetch.ScopeType != connector.ScopeTypeDelta || req.Fetch.Cursor != "cursor-1" {
		t.Fatalf("unexpected delta request: %+v", req)
	}
	if err := strategy.ObservePage(context.Background(), connector.RawObjectPage{Items: []connector.RawObject{{ObjectKey: "doc-1", DeletedAtSource: testTimePtr()}}}); err != nil {
		t.Fatalf("observe delta page: %v", err)
	}
	coverage := strategy.Coverage()
	if !coverage.Complete || coverage.CoveredTargetRoot || len(coverage.CoveredObjectKeys) != 1 || coverage.CoveredObjectKeys[0] != "doc-1" {
		t.Fatalf("unexpected delta coverage: %+v", coverage)
	}
}

func TestWatchStrategyCoversOnlyEventObject(t *testing.T) {
	t.Parallel()

	strategy := NewWatchEventCrawlStrategy(strategyInput{
		binding:  strategyBinding(),
		claim:    strategyClaim(connector.ScopeTypeWatchEvent, connector.ScopeRef{"object_key": "doc-1"}),
		pageSize: 10,
	})
	req, done, err := strategy.NextRequest(context.Background(), CrawlLoopState{})
	if err != nil || done {
		t.Fatalf("next watch request done=%v err=%v", done, err)
	}
	if req.Kind != CrawlRequestKindFetch || req.Fetch.ScopeType != connector.ScopeTypeWatchEvent || req.Fetch.ScopeRef["object_key"] != "doc-1" {
		t.Fatalf("unexpected watch request: %+v", req)
	}
	if err := strategy.ObservePage(context.Background(), connector.RawObjectPage{}); err != nil {
		t.Fatalf("observe watch page: %v", err)
	}
	coverage := strategy.Coverage()
	if !coverage.Complete || coverage.CoveredTargetRoot || len(coverage.CoveredObjectKeys) != 0 {
		t.Fatalf("watch scope_ref must not imply delete coverage: %+v", coverage)
	}

	strategy = NewWatchEventCrawlStrategy(strategyInput{
		binding:  strategyBinding(),
		claim:    strategyClaim(connector.ScopeTypeWatchEvent, connector.ScopeRef{"object_key": "doc-1"}),
		pageSize: 10,
	})
	if err := strategy.ObservePage(context.Background(), connector.RawObjectPage{Items: []connector.RawObject{{ObjectKey: "doc-1", DeletedAtSource: testTimePtr()}}}); err != nil {
		t.Fatalf("observe watch delete page: %v", err)
	}
	coverage = strategy.Coverage()
	if !coverage.Complete || coverage.CoveredTargetRoot || len(coverage.CoveredObjectKeys) != 1 || coverage.CoveredObjectKeys[0] != "doc-1" {
		t.Fatalf("unexpected explicit watch delete coverage: %+v", coverage)
	}
}

func strategyBinding() store.Binding {
	return store.Binding{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		TargetType:        "target",
		TargetRef:         "root-ref",
		TreeKey:           "root",
		BindingGeneration: 1,
	}
}

func strategyClaim(scopeType connector.ScopeType, scopeRef connector.ScopeRef) BindingRunClaim {
	return BindingRunClaim{
		RunID:             "run-1",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ScopeType:         scopeType,
		ScopeRef:          scopeRef,
	}
}

func testTimePtr() *time.Time {
	now := time.Date(2026, 5, 28, 9, 0, 0, 0, time.UTC)
	return &now
}
