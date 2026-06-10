package feishu

import (
	"context"
	"strings"

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
		if req.TargetType == TargetTypeDriveFolder && scopedDriveObjectKey(req.ScopeRef) != "" {
			return c.fetchWatchObject(ctx, token, req)
		}
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
		if objectKey := scopedDriveObjectKey(req.ScopeRef); objectKey != "" {
			return c.findDriveObject(ctx, token, req.TargetRef, objectKey)
		}
		return c.api.GetDriveFolder(ctx, token, driveFolderToken(nodeRef))
	case TargetTypeWikiNode:
		if nodeRef == VirtualWikiSpacesRef {
			return Object{
				Kind:        ObjectKindVirtualRoot,
				Token:       VirtualWikiSpacesRef,
				Name:        "Wiki",
				IsContainer: true,
				HasChildren: true,
				Revision:    "virtual-wiki",
			}, nil
		}
		if spaceID, ok := wikiSpaceID(nodeRef); ok {
			return c.getWikiSpace(ctx, token, spaceID)
		}
		spaceID, nodeToken, err := wikiNode(nodeRef)
		if err != nil {
			return Object{}, err
		}
		return c.api.GetWikiNode(ctx, token, spaceID, nodeToken)
	default:
		return Object{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
}

func (c *FeishuConnector) findDriveObject(ctx context.Context, token, targetRef, objectToken string) (Object, error) {
	objectToken = strings.TrimSpace(objectToken)
	if objectToken == "" {
		return Object{}, connector.NewError(connector.ErrorCodeInvalidArgument, "object_key is required")
	}
	rootToken := driveFolderToken(targetRef)
	if rootToken == objectToken {
		return c.api.GetDriveFolder(ctx, token, rootToken)
	}
	queue := []string{rootToken}
	seen := map[string]struct{}{}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return Object{}, err
		}
		folderToken := queue[0]
		queue = queue[1:]
		if _, ok := seen[folderToken]; ok {
			continue
		}
		seen[folderToken] = struct{}{}
		cursor := ""
		for {
			page, err := c.api.ListDriveChildren(ctx, token, folderToken, cursor, c.Spec().MaxPageSize)
			if err != nil {
				return Object{}, err
			}
			for _, item := range page.Items {
				if driveObjectMatches(item, objectToken) {
					return item, nil
				}
				if item.Kind == ObjectKindDriveFolder && strings.TrimSpace(item.Token) != "" {
					queue = append(queue, item.Token)
				}
			}
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
	}
	return Object{}, connector.NewError(connector.ErrorCodeNotFound, "drive object is not found in target")
}

func driveObjectMatches(object Object, token string) bool {
	token = strings.TrimSpace(token)
	return token != "" && (strings.TrimSpace(object.Token) == token || strings.TrimSpace(object.StableID) == token)
}

func scopedDriveObjectKey(scopeRef connector.ScopeRef) string {
	objectKey := strings.TrimSpace(scopeRef["object_key"])
	if strings.HasPrefix(objectKey, string(ConnectorType)+":drive:") {
		return strings.TrimPrefix(objectKey, string(ConnectorType)+":drive:")
	}
	return ""
}
