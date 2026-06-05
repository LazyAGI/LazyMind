package localfs

import (
	"context"
	"slices"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *LocalFSConnector) validateListRequest(req connector.ListChildrenRequest) error {
	if !isInitialRootRequest(req) {
		if err := validateTarget(req.TargetType, req.TargetRef); err != nil {
			return err
		}
	} else if req.TargetType != "" && req.TargetType != TargetTypeLocalPath {
		return connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
	if c.agent == nil && (!isInitialRootRequest(req) || len(c.recommendedRoots) == 0) {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "local_fs agent client is not configured")
	}
	if req.AgentID == "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "agent_id is required")
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

func isInitialRootRequest(req connector.ListChildrenRequest) bool {
	return req.TargetRef == "" && req.NodeRef == ""
}

func (c *LocalFSConnector) listInitialRoots(ctx context.Context, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	roots, err := c.initialRootInfos(ctx, req)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	items := make([]connector.RawObject, 0, len(roots))
	for _, root := range roots {
		if normalized, err := c.validateProbedPath(root); err == nil {
			items = append(items, c.rawObject(req.AgentID, normalized, ""))
		}
	}
	slices.SortFunc(items, func(a, b connector.RawObject) int {
		if a.DisplayName == b.DisplayName {
			return strings.Compare(a.ObjectRef, b.ObjectRef)
		}
		return strings.Compare(a.DisplayName, b.DisplayName)
	})
	page, err := sliceRawObjects(items, req.Cursor, req.PageSize)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	page.ListComplete = !page.HasMore
	return page, nil
}

func (c *LocalFSConnector) initialRootInfos(ctx context.Context, req connector.ListChildrenRequest) ([]PathInfo, error) {
	roots := make([]PathInfo, 0, len(c.recommendedRoots))
	for _, path := range c.recommendedRoots {
		publicPath := c.publicPath(path)
		roots = append(roots, PathInfo{Path: publicPath, NormalizedPath: publicPath, DisplayName: displayName("", publicPath), Exists: true, Readable: true, IsDir: true})
	}
	if c.agent != nil && len(c.recommendedRoots) > 0 {
		for i, root := range roots {
			info, err := c.agent.ValidatePath(ctx, ValidatePathRequest{AgentID: req.AgentID, Path: root.NormalizedPath, UserID: req.ProviderOptions.String("user_id")})
			if err != nil {
				return nil, err
			}
			roots[i] = info
		}
	}
	if agentRoots, ok := c.agent.(RootListingAgent); ok {
		listed, err := agentRoots.ListRoots(ctx, ListRootsRequest{AgentID: req.AgentID, UserID: req.ProviderOptions.String("user_id")})
		if err != nil {
			return nil, err
		}
		roots = append(roots, listed...)
	}
	return dedupePathInfos(roots), nil
}

func (c *LocalFSConnector) decodeNodeRef(targetRef, nodeRef string) (string, error) {
	if nodeRef == "" {
		return c.publicPath(targetRef), nil
	}
	if keyPath, ok := pathFromObjectKey(nodeRef); ok {
		if err := c.rejectOutsidePublicRoot(keyPath); err != nil {
			return "", err
		}
		return keyPath, nil
	}
	return c.publicPath(nodeRef), nil
}

func (c *LocalFSConnector) listOnePage(ctx context.Context, req connector.ListChildrenRequest, nodePath, cursor string) (connector.RawObjectPage, error) {
	page, err := c.agent.ListDir(ctx, ListDirRequest{
		AgentID:      req.AgentID,
		Path:         nodePath,
		Cursor:       cursor,
		PageSize:     req.PageSize,
		IncludeFiles: true,
	})
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.buildRawObjectPage(req.AgentID, nodePath, page, !page.HasMore), nil
}

func (c *LocalFSConnector) listAllCurrentLevel(ctx context.Context, req connector.ListChildrenRequest, nodePath string) (connector.RawObjectPage, error) {
	var items []connector.RawObject
	cursor := ""
	for {
		page, err := c.listOnePage(ctx, req, nodePath, cursor)
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

func (c *LocalFSConnector) buildRawObjectPage(agentID, parentPath string, page ListDirPage, complete bool) connector.RawObjectPage {
	items := make([]connector.RawObject, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, c.rawObject(agentID, item, parentPath))
	}
	return connector.RawObjectPage{
		Items:        dedupeRawObjects(items),
		HasMore:      page.HasMore,
		NextCursor:   page.NextCursor,
		ListComplete: complete,
	}
}

func (c *LocalFSConnector) virtualTargetPage(page connector.RawObjectPage) connector.RawObjectPage {
	if c.publicRoot == "" {
		return page
	}
	for i := range page.Items {
		page.Items[i] = c.virtualTargetObject(page.Items[i])
	}
	return page
}

func (c *LocalFSConnector) virtualTargetObject(raw connector.RawObject) connector.RawObject {
	raw.ObjectRef = c.virtualPath(raw.ObjectRef)
	raw.ParentRef = c.virtualPath(raw.ParentRef)
	if raw.BindingTargetRef != "" {
		raw.BindingTargetRef = c.virtualPath(raw.BindingTargetRef)
	}
	return raw
}
