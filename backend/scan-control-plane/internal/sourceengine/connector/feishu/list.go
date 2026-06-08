package feishu

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *FeishuConnector) validateListRequest(req connector.ListChildrenRequest) error {
	if !isInitialRootRequest(req) && !isVirtualBranchRequest(req.NodeRef) {
		if err := validateTarget(req.TargetType, req.TargetRef); err != nil {
			return err
		}
	} else if req.TargetType != "" && !isSupportedTargetType(req.TargetType) {
		return connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
	if req.AuthConnectionID == "" && !isInitialRootRequest(req) {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "auth_connection_id is required")
	}
	if c.auth == nil || c.api == nil {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "feishu clients are not configured")
	}
	if req.ListMode == "" {
		req.ListMode = connector.ListModePage
	}
	if req.ListMode != connector.ListModePage && req.ListMode != connector.ListModeAllCurrentLevel {
		return connector.NewError(connector.ErrorCodeUnsupportedListMode, "list_mode is not supported")
	}
	if req.ListMode == connector.ListModeAllCurrentLevel && req.Cursor != "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "cursor must be empty for all_current_level")
	}
	return validatePageSize(req.PageSize, c.Spec().MaxPageSize)
}

func (c *FeishuConnector) listOnePage(ctx context.Context, token string, req connector.ListChildrenRequest, cursor string) (connector.RawObjectPage, error) {
	page, err := c.listProviderPage(ctx, token, req.TargetType, req.TargetRef, req.NodeRef, cursor, providerPageSize(req.TargetType, req.NodeRef, req.PageSize))
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.buildRawObjectPage(req.AuthConnectionID, page, !page.HasMore), nil
}

func (c *FeishuConnector) listAllCurrentLevel(ctx context.Context, token string, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	var items []connector.RawObject
	cursor := ""
	for {
		page, err := c.listOnePage(ctx, token, req, cursor)
		if err != nil {
			return connector.RawObjectPage{}, err
		}
		items = append(items, page.Items...)
		items = dedupeRawObjects(items)
		if len(items) > req.MaxItems {
			return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeResultTooLarge, "current level has more items than max_items")
		}
		if !page.HasMore {
			return connector.RawObjectPage{Items: items, ListComplete: true}, nil
		}
		cursor = page.NextCursor
	}
}

func (c *FeishuConnector) listProviderPage(ctx context.Context, token string, targetType connector.TargetType, targetRef, nodeRef, cursor string, pageSize int) (ObjectPage, error) {
	if nodeRef == "" && targetRef == "" {
		return virtualRootPage(cursor, pageSize)
	}
	switch targetType {
	case TargetTypeDriveFolder:
		if nodeRef == VirtualDriveRootRef {
			return c.listDriveRootChildren(ctx, token, cursor, pageSize)
		}
		folderToken := driveFolderToken(firstNonEmpty(nodeRef, targetRef))
		return c.api.ListDriveChildren(ctx, token, folderToken, cursor, pageSize)
	case TargetTypeWikiNode:
		if nodeRef == VirtualWikiSpacesRef {
			return c.api.ListWikiSpaces(ctx, token, cursor, pageSize)
		}
		if spaceID, ok := wikiSpaceID(nodeRef); ok {
			return c.api.ListWikiChildren(ctx, token, spaceID, "", cursor, pageSize)
		}
		spaceID, nodeToken, err := wikiNode(firstNonEmpty(nodeRef, targetRef))
		if err != nil {
			return ObjectPage{}, err
		}
		return c.api.ListWikiChildren(ctx, token, spaceID, nodeToken, cursor, pageSize)
	default:
		return ObjectPage{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
}

func providerPageSize(targetType connector.TargetType, nodeRef string, pageSize int) int {
	if targetType == TargetTypeWikiNode {
		return min(pageSize, 50)
	}
	return pageSize
}

func (c *FeishuConnector) listDriveRootChildren(ctx context.Context, token, cursor string, pageSize int) (ObjectPage, error) {
	root, err := c.api.GetDriveRoot(ctx, token)
	if err != nil {
		return ObjectPage{}, err
	}
	return c.api.ListDriveChildren(ctx, token, root.Token, cursor, pageSize)
}

func (c *FeishuConnector) buildRawObjectPage(authConnectionID string, page ObjectPage, complete bool) connector.RawObjectPage {
	items := make([]connector.RawObject, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, c.rawObject(authConnectionID, item))
	}
	return connector.RawObjectPage{
		Items:        dedupeRawObjects(items),
		HasMore:      page.HasMore,
		NextCursor:   page.NextCursor,
		Watermark:    page.Watermark,
		ListComplete: complete,
	}
}
