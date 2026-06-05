package feishu

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *FeishuConnector) validateFetchRequest(req connector.FetchPageRequest) error {
	if err := validateTarget(req.TargetType, req.TargetRef); err != nil {
		return err
	}
	if req.AuthConnectionID == "" {
		return connector.NewError(ErrorCodeAuthInvalid, "auth_connection_id is required")
	}
	if req.BindingGeneration <= 0 {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "binding_generation must be positive")
	}
	return validatePageSize(req.PageSize, c.Spec().MaxPageSize)
}

func (c *FeishuConnector) fetchOnePage(ctx context.Context, token string, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	switch req.ScopeType {
	case connector.ScopeTypeFull:
		return c.fetchListPage(ctx, token, req)
	case connector.ScopeTypePartial:
		if req.TargetType == TargetTypeWikiNode && scopeNodeRef(req.ScopeRef) != "" {
			return c.fetchWatchObject(ctx, token, req)
		}
		return c.fetchListPage(ctx, token, req)
	case connector.ScopeTypeWatchEvent:
		return c.fetchWatchObject(ctx, token, req)
	case connector.ScopeTypeDelta:
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupportedDelta, "feishu delta fetch is not supported")
	default:
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "scope_type is not supported")
	}
}

func (c *FeishuConnector) fetchListPage(ctx context.Context, token string, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	targetRef := req.TargetRef
	if scoped := scopeNodeRef(req.ScopeRef); scoped != "" {
		targetRef = scoped
	}
	page, err := c.listProviderPage(ctx, token, req.TargetType, req.TargetRef, targetRef, req.Cursor, providerPageSize(req.TargetType, targetRef, req.PageSize))
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.buildRawObjectPage(req.AuthConnectionID, page, !page.HasMore), nil
}

func (c *FeishuConnector) fetchWatchObject(ctx context.Context, token string, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	object, err := c.loadScopedObject(ctx, token, req)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	raw := c.rawObject(req.AuthConnectionID, object)
	return connector.RawObjectPage{Items: []connector.RawObject{raw}, Watermark: raw.SourceVersion, ListComplete: true}, nil
}

func (c *FeishuConnector) loadScopedObject(ctx context.Context, token string, req connector.FetchPageRequest) (Object, error) {
	nodeRef := scopeNodeRef(req.ScopeRef)
	if nodeRef == "" {
		nodeRef = req.TargetRef
	}
	switch req.TargetType {
	case TargetTypeDriveFolder:
		return c.api.GetDriveFolder(ctx, token, driveFolderToken(nodeRef))
	case TargetTypeWikiNode:
		spaceID, nodeToken, err := wikiNode(nodeRef)
		if err != nil {
			return Object{}, err
		}
		return c.api.GetWikiNode(ctx, token, spaceID, nodeToken)
	default:
		return Object{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
}
