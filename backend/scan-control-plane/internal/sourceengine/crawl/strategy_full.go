package crawl

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type FullCrawlStrategy struct {
	strategyBase
	recursive bool
	fetchRoot bool
	rootDone  bool
	queue     []string
	active    bfsContainer
}

type bfsContainer struct {
	nodeRef string
	cursor  string
	set     bool
}

func NewFullCrawlStrategy(input strategyInput) *FullCrawlStrategy {
	s := &FullCrawlStrategy{
		strategyBase: newStrategyBase(input, connector.ScopeTypeFull),
		recursive:    input.spec.SupportsRecursiveFetch,
		fetchRoot:    !input.spec.SupportsRecursiveFetch && input.spec.SupportsDualRoleObject,
	}
	if !s.recursive {
		s.queue = []string{""}
	}
	return s
}

func (s *FullCrawlStrategy) NextRequest(ctx context.Context, _ CrawlLoopState) (CrawlRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return CrawlRequest{}, false, err
	}
	if s.recursive {
		return s.nextRecursiveRequest()
	}
	return s.nextBFSRequest()
}

func (s *FullCrawlStrategy) ObservePage(ctx context.Context, page connector.RawObjectPage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.recursive {
		s.observePage(page)
		return nil
	}
	s.observeBFSPage(page)
	return nil
}

func (s *FullCrawlStrategy) nextRecursiveRequest() (CrawlRequest, bool, error) {
	if s.done {
		return CrawlRequest{}, true, nil
	}
	return CrawlRequest{
		Kind:  CrawlRequestKindFetch,
		Fetch: s.fetchRequest(connector.ScopeTypeFull, s.cursor),
	}, false, nil
}

func (s *FullCrawlStrategy) nextBFSRequest() (CrawlRequest, bool, error) {
	if s.fetchRoot && !s.rootDone {
		return CrawlRequest{Kind: CrawlRequestKindFetch, Fetch: s.fetchRequest(connector.ScopeTypeWatchEvent, "")}, false, nil
	}
	if !s.active.set {
		if len(s.queue) == 0 {
			s.done = true
			return CrawlRequest{}, true, nil
		}
		s.active = bfsContainer{nodeRef: s.queue[0], set: true}
		s.queue = s.queue[1:]
	}
	return CrawlRequest{
		Kind:         CrawlRequestKindListChildren,
		ListChildren: s.listRequest(s.active.nodeRef, s.active.cursor),
	}, false, nil
}

func (s *FullCrawlStrategy) observeBFSPage(page connector.RawObjectPage) {
	s.builder.observePage(page)
	if s.fetchRoot && !s.rootDone {
		s.rootDone = true
		return
	}
	for _, raw := range page.Items {
		if raw.IsContainer || raw.HasChildren {
			s.queue = append(s.queue, nodeRefForRaw(raw))
		}
	}
	if page.HasMore {
		s.active.cursor = page.NextCursor
		s.cursor = page.NextCursor
		return
	}
	s.active = bfsContainer{}
	s.cursor = ""
	s.done = len(s.queue) == 0
}
