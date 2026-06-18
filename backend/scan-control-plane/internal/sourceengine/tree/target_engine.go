package tree

import "github.com/lazymind/scan_control_plane/internal/sourceengine/connector"

type DefaultTargetTreeEngine struct {
	registry connector.ConnectorRegistry
	fallback TargetTreeFallbackSearch
	limits   TreeQueryLimits
	cache    *targetSearchCache
}

type TargetTreeOption func(*DefaultTargetTreeEngine)

func NewDefaultTargetTreeEngine(registry connector.ConnectorRegistry, options ...TargetTreeOption) *DefaultTargetTreeEngine {
	e := &DefaultTargetTreeEngine{
		registry: registry,
		limits:   defaultLimits(TreeQueryLimits{}),
		cache:    newTargetSearchCache(),
	}
	for _, option := range options {
		option(e)
	}
	e.limits = defaultLimits(e.limits)
	return e
}

func WithTargetTreeLimits(limits TreeQueryLimits) TargetTreeOption {
	return func(e *DefaultTargetTreeEngine) {
		e.limits = limits
	}
}

func WithFallbackSearch(fallback TargetTreeFallbackSearch) TargetTreeOption {
	return func(e *DefaultTargetTreeEngine) {
		e.fallback = fallback
	}
}

func WithTargetSearchCacheStore(store targetSearchCacheStore) TargetTreeOption {
	return func(e *DefaultTargetTreeEngine) {
		if e.cache != nil {
			e.cache.store = store
		}
	}
}
