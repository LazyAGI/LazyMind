package tree

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (e *DefaultTargetTreeEngine) Search(ctx context.Context, req TargetTreeSearchRequest) (TreeNodePage, error) {
	if strings.TrimSpace(req.Keyword) == "" {
		return TreeNodePage{}, NewError(ErrCodeInvalidRequest, "keyword is required")
	}
	if err := validateSearchListMode(req.ListMode); err != nil {
		return TreeNodePage{}, err
	}
	conn, err := e.registry.Get(req.ConnectorType)
	if err != nil {
		return TreeNodePage{}, mapConnectorError(err)
	}
	if !conn.Spec().SupportsSearch {
		if e.fallback == nil {
			return TreeNodePage{}, NewError(ErrCodeInvalidRequest, "connector does not support search")
		}
		return e.fallback.Search(ctx, req)
	}
	pageSize := normalizePageSize(req.PageSize, e.limitForConnector(conn.Spec()))
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
	return e.mapTargetSearchPage(ctx, conn, req, rawPage)
}

func (e *DefaultTargetTreeEngine) mapTargetSearchPage(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest, rawPage connector.RawObjectPage) (TreeNodePage, error) {
	nodes := make([]TreeNode, 0, len(rawPage.Items))
	for _, raw := range rawPage.Items {
		normalized, err := conn.MapObject(ctx, raw)
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		if !req.IncludeFiles && !isTargetDirectoryNode(raw, normalized) {
			continue
		}
		if !targetSearchMatches(normalized, req.Keyword) {
			continue
		}
		nodes = append(nodes, targetNode(req.ConnectorType, raw, normalized))
	}
	return TreeNodePage{
		Items:        nodes,
		NextCursor:   rawPage.NextCursor,
		HasMore:      rawPage.HasMore,
		ListComplete: rawPage.ListComplete,
		SearchMode:   SearchModeConnector,
	}, nil
}

func targetSearchMatches(normalized connector.NormalizedSourceObject, keyword string) bool {
	needle := strings.ToLower(strings.TrimSpace(keyword))
	if needle == "" {
		return true
	}
	for _, value := range []string{normalized.SearchName, normalized.DisplayName} {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}
