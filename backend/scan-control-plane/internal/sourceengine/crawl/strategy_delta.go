package crawl

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type DeltaCrawlStrategy struct {
	strategyBase
	supported bool
}

func NewDeltaCrawlStrategy(input strategyInput) *DeltaCrawlStrategy {
	return &DeltaCrawlStrategy{
		strategyBase: newStrategyBase(input, connector.ScopeTypeDelta),
		supported:    input.spec.SupportsDelta,
	}
}

func (s *DeltaCrawlStrategy) NextRequest(ctx context.Context, _ CrawlLoopState) (CrawlRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return CrawlRequest{}, false, err
	}
	if !s.supported {
		return CrawlRequest{}, false, errDeltaUnsupported
	}
	if s.done {
		return CrawlRequest{}, true, nil
	}
	return CrawlRequest{
		Kind:  CrawlRequestKindFetch,
		Fetch: s.fetchRequest(connector.ScopeTypeDelta, s.cursor),
	}, false, nil
}

func (s *DeltaCrawlStrategy) ObservePage(ctx context.Context, page connector.RawObjectPage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.observePage(page)
	return nil
}
