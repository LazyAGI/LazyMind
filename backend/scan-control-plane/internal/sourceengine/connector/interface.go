package connector

import "context"

type SourceConnector interface {
	Spec() ConnectorSpec
	ValidateTarget(ctx context.Context, req ValidateTargetRequest) (NormalizedTarget, error)
	ListChildren(ctx context.Context, req ListChildrenRequest) (RawObjectPage, error)
	Search(ctx context.Context, req SearchRequest) (RawObjectPage, error)
	FetchPage(ctx context.Context, req FetchPageRequest) (RawObjectPage, error)
	ExportObject(ctx context.Context, req ExportObjectRequest) (ExportedObject, error)
	MapObject(ctx context.Context, raw RawObject) (NormalizedSourceObject, error)
}

type ConnectorRegistry interface {
	Register(connector SourceConnector) error
	Get(connectorType ConnectorType) (SourceConnector, error)
	Specs() []ConnectorSpec
}
