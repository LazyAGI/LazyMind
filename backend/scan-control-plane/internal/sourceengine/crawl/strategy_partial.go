package crawl

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type PartialCrawlStrategy struct {
	*FullCrawlStrategy
	fetchOne bool
}

func NewPartialCrawlStrategy(input strategyInput) *PartialCrawlStrategy {
	input.claim.ScopeType = connector.ScopeTypePartial
	full := NewFullCrawlStrategy(input)
	full.builder = newCoverageBuilder(connector.ScopeTypePartial, input.claim.ScopeRef)
	full.recursive = input.spec.SupportsRecursiveFetch
	fetchOne := firstScopeValue(input.claim.ScopeRef, "object_key", "path") != ""
	if !full.recursive {
		full.queue = partialQueue(input.binding.TargetRef, input.binding.TreeKey, input.claim.ScopeRef)
	}
	return &PartialCrawlStrategy{FullCrawlStrategy: full, fetchOne: fetchOne}
}

func (s *PartialCrawlStrategy) NextRequest(ctx context.Context, state CrawlLoopState) (CrawlRequest, bool, error) {
	if s.fetchOne && !s.recursive {
		if s.done {
			return CrawlRequest{}, true, nil
		}
		s.done = true
		return CrawlRequest{Kind: CrawlRequestKindFetch, Fetch: s.fetchRequest(connector.ScopeTypePartial, "")}, false, ctx.Err()
	}
	if s.recursive {
		if s.done {
			return CrawlRequest{}, true, nil
		}
		return CrawlRequest{Kind: CrawlRequestKindFetch, Fetch: s.fetchRequest(connector.ScopeTypePartial, s.cursor)}, false, ctx.Err()
	}
	return s.FullCrawlStrategy.NextRequest(ctx, state)
}

func (s *PartialCrawlStrategy) ObservePage(ctx context.Context, page connector.RawObjectPage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.recursive || s.fetchOne {
		s.observePage(page)
		return nil
	}
	s.observeBFSPage(page)
	return nil
}

func partialQueue(_ string, _ string, scopeRef connector.ScopeRef) []string {
	if root := firstScopeValue(scopeRef, "node_ref", "object_ref", "subtree_root", "root_object_key", "object_key"); root != "" {
		return []string{root}
	}
	return []string{""}
}
