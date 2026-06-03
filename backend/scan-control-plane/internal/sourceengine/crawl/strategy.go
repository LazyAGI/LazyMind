package crawl

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

var errDeltaUnsupported = connector.NewError(connector.ErrorCodeUnsupportedDelta, "delta crawl is not supported")

type CrawlStrategy interface {
	NextRequest(ctx context.Context, state CrawlLoopState) (CrawlRequest, bool, error)
	ObservePage(ctx context.Context, page connector.RawObjectPage) error
	Coverage() Coverage
	NextCursor() string
}

type strategyInput struct {
	binding  store.Binding
	conn     connector.SourceConnector
	spec     connector.ConnectorSpec
	claim    BindingRunClaim
	pageSize int
}

type CrawlStrategyFactory interface {
	Strategy(input strategyInput) (CrawlStrategy, error)
}

type defaultStrategyFactory struct{}

func (defaultStrategyFactory) Strategy(input strategyInput) (CrawlStrategy, error) {
	switch input.claim.ScopeType {
	case connector.ScopeTypeFull:
		return NewFullCrawlStrategy(input), nil
	case connector.ScopeTypePartial:
		return NewPartialCrawlStrategy(input), nil
	case connector.ScopeTypeDelta:
		return NewDeltaCrawlStrategy(input), nil
	case connector.ScopeTypeWatchEvent:
		return NewWatchEventCrawlStrategy(input), nil
	default:
		return nil, connector.NewError(connector.ErrorCodeUnsupported, "scope_type is not supported")
	}
}

type strategyBase struct {
	binding  store.Binding
	claim    BindingRunClaim
	pageSize int
	builder  *coverageBuilder
	cursor   string
	done     bool
}

func newStrategyBase(input strategyInput, scopeType connector.ScopeType) strategyBase {
	claim := input.claim
	claim.ScopeType = scopeType
	return strategyBase{
		binding:  input.binding,
		claim:    claim,
		pageSize: input.pageSize,
		builder:  newCoverageBuilder(scopeType, claim.ScopeRef),
		cursor:   claim.Cursor,
	}
}

func (s *strategyBase) Coverage() Coverage {
	if s.done {
		return s.builder.complete()
	}
	return s.builder.incomplete("crawl_not_complete")
}

func (s *strategyBase) NextCursor() string {
	return s.cursor
}

func (s *strategyBase) observePage(page connector.RawObjectPage) {
	s.builder.observePage(page)
	if page.NextCursor != "" {
		s.cursor = page.NextCursor
	}
	if !page.HasMore {
		s.done = true
	}
}

func (s *strategyBase) fetchRequest(scopeType connector.ScopeType, cursor string) connector.FetchPageRequest {
	return connector.FetchPageRequest{
		SourceID:          s.claim.SourceID,
		BindingID:         s.claim.BindingID,
		BindingGeneration: s.claim.BindingGeneration,
		TargetType:        connector.TargetType(s.binding.TargetType),
		TargetRef:         s.binding.TargetRef,
		ScopeType:         scopeType,
		ScopeRef:          s.claim.ScopeRef,
		Cursor:            cursor,
		PageSize:          s.pageSize,
		AgentID:           s.binding.AgentID,
		AuthConnectionID:  s.binding.AuthConnectionID,
		ProviderOptions:   providerOptions(s.binding.ProviderOptions),
	}
}

func (s *strategyBase) listRequest(nodeRef, cursor string) connector.ListChildrenRequest {
	return connector.ListChildrenRequest{
		TargetType:       connector.TargetType(s.binding.TargetType),
		TargetRef:        s.binding.TargetRef,
		NodeRef:          nodeRef,
		ListMode:         connector.ListModePage,
		Cursor:           cursor,
		PageSize:         s.pageSize,
		AgentID:          s.binding.AgentID,
		AuthConnectionID: s.binding.AuthConnectionID,
		ProviderOptions:  providerOptions(s.binding.ProviderOptions),
	}
}

func nodeRefForRaw(raw connector.RawObject) string {
	if raw.ObjectRef != "" {
		return raw.ObjectRef
	}
	return raw.ObjectKey
}
