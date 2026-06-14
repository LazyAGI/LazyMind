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
	rawPage, err := conn.Search(ctx, connector.SearchRequest{
		TargetType:       req.TargetType,
		TargetRef:        req.TargetRef,
		NodeRef:          req.NodeRef,
		Keyword:          req.Keyword,
		Cursor:           req.Cursor,
		PageSize:         pageSize,
		AgentID:          req.AgentID,
		AuthConnectionID: req.AuthConnectionID,
		ProviderOptions:  connector.ProviderOptions(req.ProviderOptions),
	})
	if err != nil {
		return TreeNodePage{}, mapConnectorError(err)
	}
	childrenReq := TargetTreeChildrenRequest{ConnectorType: req.ConnectorType, IncludeFiles: true}
	return e.mapTargetPage(ctx, conn, childrenReq, rawPage, SearchModeConnector)
}
