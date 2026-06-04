package tree

import "github.com/lazymind/scan_control_plane/internal/sourceengine/connector"

type DBSourceTreeQueryEngine struct {
	repo     SourceTreeReadRepository
	registry connector.ConnectorRegistry
	limits   TreeQueryLimits
}

type SourceTreeOption func(*DBSourceTreeQueryEngine)

func NewDBSourceTreeQueryEngine(repo SourceTreeReadRepository, limits TreeQueryLimits, options ...SourceTreeOption) *DBSourceTreeQueryEngine {
	e := &DBSourceTreeQueryEngine{repo: repo, limits: defaultLimits(limits)}
	for _, option := range options {
		option(e)
	}
	e.limits = defaultLimits(e.limits)
	return e
}

func WithSourceTreeConnectorRegistry(registry connector.ConnectorRegistry) SourceTreeOption {
	return func(e *DBSourceTreeQueryEngine) {
		e.registry = registry
	}
}
