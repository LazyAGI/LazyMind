package connector

import (
	"sort"
)

type DefaultConnectorRegistry struct {
	connectors map[ConnectorType]SourceConnector
}

func NewDefaultConnectorRegistry(connectors ...SourceConnector) (*DefaultConnectorRegistry, error) {
	registry := &DefaultConnectorRegistry{connectors: make(map[ConnectorType]SourceConnector)}
	for _, connector := range connectors {
		if err := registry.Register(connector); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *DefaultConnectorRegistry) Register(connector SourceConnector) error {
	r.ensureConnectors()
	if connector == nil {
		return NewError(ErrorCodeInvalidArgument, "connector is nil")
	}
	spec := connector.Spec()
	if err := validateSpec(spec); err != nil {
		return err
	}
	if _, exists := r.connectors[spec.ConnectorType]; exists {
		return NewError(ErrorCodeAlreadyExists, "connector is already registered")
	}
	r.connectors[spec.ConnectorType] = connector
	return nil
}

func (r *DefaultConnectorRegistry) Get(connectorType ConnectorType) (SourceConnector, error) {
	connector, ok := r.connectors[connectorType]
	if !ok {
		return nil, NewError(ErrorCodeNotFound, "connector is not registered")
	}
	return connector, nil
}

func (r *DefaultConnectorRegistry) Specs() []ConnectorSpec {
	specs := make([]ConnectorSpec, 0, len(r.connectors))
	for _, connector := range r.connectors {
		specs = append(specs, connector.Spec())
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ConnectorType < specs[j].ConnectorType
	})
	return specs
}

func (r *DefaultConnectorRegistry) ensureConnectors() {
	if r.connectors == nil {
		r.connectors = make(map[ConnectorType]SourceConnector)
	}
}

func validateSpec(spec ConnectorSpec) error {
	if spec.ConnectorType == "" {
		return NewError(ErrorCodeInvalidArgument, "connector_type is required")
	}
	if len(spec.TargetTypes) == 0 {
		return NewError(ErrorCodeInvalidArgument, "target_types is required")
	}
	if spec.MaxPageSize <= 0 {
		return NewError(ErrorCodeInvalidArgument, "max_page_size must be positive")
	}
	return nil
}
