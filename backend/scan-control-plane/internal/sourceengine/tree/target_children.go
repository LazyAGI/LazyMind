package tree

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/filefilter"
)

func (e *DefaultTargetTreeEngine) ListChildren(ctx context.Context, req TargetTreeChildrenRequest) (TreeNodePage, error) {
	conn, err := e.registry.Get(req.ConnectorType)
	if err != nil {
		return TreeNodePage{}, mapConnectorError(err)
	}
	listMode, err := validateListMode(req.ListMode, req.Cursor, req.MaxItems, e.limits)
	if err != nil {
		return TreeNodePage{}, err
	}
	pageSize := normalizePageSize(req.PageSize, e.limitForConnector(conn.Spec()))
	if listMode == ListModeAllCurrentLevel {
		return e.listAllCurrentLevel(ctx, conn, req, pageSize)
	}
	rawPage, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType:       req.TargetType,
		TargetRef:        req.TargetRef,
		NodeRef:          req.NodeRef,
		ListMode:         connector.ListModePage,
		Cursor:           req.Cursor,
		PageSize:         pageSize,
		AgentID:          req.AgentID,
		AuthConnectionID: req.AuthConnectionID,
		ProviderOptions:  connector.ProviderOptions(req.ProviderOptions),
	})
	if err != nil {
		return TreeNodePage{}, mapConnectorError(err)
	}
	return e.mapTargetPage(ctx, conn, req, rawPage, SearchModeConnector)
}

func (e *DefaultTargetTreeEngine) listAllCurrentLevel(ctx context.Context, conn connector.SourceConnector, req TargetTreeChildrenRequest, pageSize int) (TreeNodePage, error) {
	var all []connector.RawObject
	cursor := ""
	for {
		rawPage, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
			TargetType:       req.TargetType,
			TargetRef:        req.TargetRef,
			NodeRef:          req.NodeRef,
			ListMode:         connector.ListModePage,
			Cursor:           cursor,
			PageSize:         pageSize,
			MaxItems:         req.MaxItems,
			AgentID:          req.AgentID,
			AuthConnectionID: req.AuthConnectionID,
			ProviderOptions:  connector.ProviderOptions(req.ProviderOptions),
		})
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		all = append(all, rawPage.Items...)
		if len(all) > req.MaxItems {
			return TreeNodePage{}, NewError(ErrCodeResultTooLarge, "current level has more items than max_items")
		}
		if !rawPage.HasMore {
			break
		}
		cursor = rawPage.NextCursor
	}
	return e.mapTargetPage(ctx, conn, req, connector.RawObjectPage{Items: all, ListComplete: true}, SearchModeConnector)
}

func (e *DefaultTargetTreeEngine) mapTargetPage(ctx context.Context, conn connector.SourceConnector, req TargetTreeChildrenRequest, rawPage connector.RawObjectPage, searchMode string) (TreeNodePage, error) {
	nodes := make([]TreeNode, 0, len(rawPage.Items))
	policy := filefilter.FromProviderOptions(req.ProviderOptions)
	for _, raw := range rawPage.Items {
		normalized, err := conn.MapObject(ctx, raw)
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		if !isTargetDirectoryNode(raw, normalized) {
			continue
		}
		if !targetAllowsNormalized(policy, normalized) {
			continue
		}
		nodes = append(nodes, targetNode(req.ConnectorType, raw, normalized))
	}
	return TreeNodePage{
		Items:        nodes,
		NextCursor:   rawPage.NextCursor,
		HasMore:      rawPage.HasMore,
		ListComplete: rawPage.ListComplete,
		SearchMode:   searchMode,
	}, nil
}

func (e *DefaultTargetTreeEngine) limitForConnector(spec connector.ConnectorSpec) TreeQueryLimits {
	limits := e.limits
	if spec.MaxPageSize > 0 && spec.MaxPageSize < limits.MaxPageSize {
		limits.MaxPageSize = spec.MaxPageSize
	}
	if limits.DefaultPageSize > limits.MaxPageSize {
		limits.DefaultPageSize = limits.MaxPageSize
	}
	return limits
}

func isTargetDirectoryNode(raw connector.RawObject, normalized connector.NormalizedSourceObject) bool {
	return normalized.IsContainer || raw.Bindable
}

func targetAllowsNormalized(policy filefilter.Policy, normalized connector.NormalizedSourceObject) bool {
	return normalized.IsContainer || normalized.HasChildren || filefilter.AllowsNormalized(policy, normalized)
}

func targetAllowsTreeNode(policy filefilter.Policy, node TreeNode) bool {
	return node.IsContainer || node.HasChildren || policy.Allows(filefilter.ObjectInfo{
		DisplayName:  node.DisplayName,
		ObjectKey:    node.ObjectKey,
		IsDocument:   node.IsDocument,
		IsContainer:  node.IsContainer,
		ProviderMeta: node.ProviderMeta,
	})
}
