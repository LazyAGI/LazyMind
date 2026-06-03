package crawl

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type WatchEventCrawlStrategy struct {
	strategyBase
}

func NewWatchEventCrawlStrategy(input strategyInput) *WatchEventCrawlStrategy {
	return &WatchEventCrawlStrategy{strategyBase: newStrategyBase(input, connector.ScopeTypeWatchEvent)}
}

func (s *WatchEventCrawlStrategy) NextRequest(ctx context.Context, _ CrawlLoopState) (CrawlRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return CrawlRequest{}, false, err
	}
	if s.done {
		return CrawlRequest{}, true, nil
	}
	return CrawlRequest{
		Kind:  CrawlRequestKindFetch,
		Fetch: s.fetchRequest(connector.ScopeTypeWatchEvent, s.cursor),
	}, false, nil
}

func (s *WatchEventCrawlStrategy) ObservePage(ctx context.Context, page connector.RawObjectPage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.observePage(page)
	return nil
}
