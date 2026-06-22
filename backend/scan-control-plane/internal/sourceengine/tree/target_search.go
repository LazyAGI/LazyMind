package tree

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/filefilter"
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
	if isLocalFSTargetSearch(req) {
		if targetSearchHasCurrentLevel(req) {
			return e.buildAndSearchCachedTargets(ctx, conn, req)
		}
		return e.searchLocalFSRootCaches(ctx, conn, req)
	}
	if !targetSearchHasCurrentLevel(req) {
		return e.searchCachedTargets(ctx, conn, req)
	}
	return e.searchCurrentLevelTargets(ctx, conn, req)
}

func (e *DefaultTargetTreeEngine) searchCurrentLevelTargets(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest) (TreeNodePage, error) {
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

func (e *DefaultTargetTreeEngine) searchConnectorTargets(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest) (TreeNodePage, error) {
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

func (e *DefaultTargetTreeEngine) searchLocalFSRootCaches(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest) (TreeNodePage, error) {
	pageSize := normalizePageSize(req.PageSize, e.limitForConnector(conn.Spec()))
	roots, err := e.listLocalFSRootTargets(ctx, conn, req)
	if err != nil {
		return TreeNodePage{}, err
	}
	nodes := make([]TreeNode, 0, len(roots))
	cacheStatus := targetSearchCacheStatusComplete
	cacheComplete := true
	cacheBuilding := false
	truncated := false
	seen := map[string]struct{}{}
	for _, root := range roots {
		normalized, err := conn.MapObject(ctx, root)
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		rootNode := targetNode(req.ConnectorType, root, normalized)
		appendUniqueTargetNode(&nodes, seen, rootNode)
		rootReq := localFSRootSearchRequest(req, root)
		snapshot := e.cache.buildIfUnlocked(ctx, conn, rootReq, e.buildTargetSearchCache)
		if snapshot.status == targetSearchCacheStatusFailed && strings.TrimSpace(snapshot.lastError) != "" {
			return TreeNodePage{}, NewError(ErrCodeInternal, "target search cache build failed: "+snapshot.lastError)
		}
		if snapshot.building || snapshot.status == targetSearchCacheStatusBuilding {
			cacheStatus = targetSearchCacheStatusBuilding
			cacheComplete = false
			cacheBuilding = true
		}
		if !snapshot.complete {
			cacheComplete = false
		}
		if snapshot.truncated {
			truncated = true
		}
		for _, node := range snapshot.nodes {
			appendUniqueTargetNode(&nodes, seen, node)
		}
	}
	page, err := paginateCachedTargetNodes(nodes, req.Keyword, req.IncludeFiles, filefilter.FromProviderOptions(req.ProviderOptions), pageSize, req.Cursor)
	if err != nil {
		return TreeNodePage{}, err
	}
	page.CacheStatus = cacheStatus
	page.CacheBuilding = cacheBuilding
	page.CacheComplete = cacheComplete
	page.Truncated = truncated
	return page, nil
}

func (e *DefaultTargetTreeEngine) listLocalFSRootTargets(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest) ([]connector.RawObject, error) {
	pageSize := e.limitForConnector(conn.Spec()).MaxPageSize
	cursor := ""
	var roots []connector.RawObject
	for {
		rawPage, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
			ListMode:        connector.ListModePage,
			Cursor:          cursor,
			PageSize:        pageSize,
			AgentID:         req.AgentID,
			ProviderOptions: connector.ProviderOptions(req.ProviderOptions),
		})
		if err != nil {
			return nil, mapConnectorError(err)
		}
		roots = append(roots, rawPage.Items...)
		if !rawPage.HasMore {
			return roots, nil
		}
		if strings.TrimSpace(rawPage.NextCursor) == "" {
			return nil, NewError(ErrCodeInternal, "local_fs root pagination cursor is empty")
		}
		cursor = rawPage.NextCursor
	}
}

func localFSRootSearchRequest(req TargetTreeSearchRequest, root connector.RawObject) TargetTreeSearchRequest {
	rootReq := req
	rootReq.Cursor = ""
	rootReq.TargetType = root.BindingTargetType
	if rootReq.TargetType == "" {
		rootReq.TargetType = req.TargetType
	}
	rootReq.TargetRef = root.BindingTargetRef
	if rootReq.TargetRef == "" {
		rootReq.TargetRef = root.ObjectRef
	}
	rootReq.NodeRef = ""
	return rootReq
}

func appendUniqueTargetNode(nodes *[]TreeNode, seen map[string]struct{}, node TreeNode) {
	key := node.ObjectKey
	if strings.TrimSpace(key) == "" {
		key = node.Key
	}
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*nodes = append(*nodes, node)
}

func (e *DefaultTargetTreeEngine) mapTargetSearchPage(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest, rawPage connector.RawObjectPage) (TreeNodePage, error) {
	nodes := make([]TreeNode, 0, len(rawPage.Items))
	policy := filefilter.FromProviderOptions(req.ProviderOptions)
	for _, raw := range rawPage.Items {
		normalized, err := conn.MapObject(ctx, raw)
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		if !targetAllowsNormalized(policy, normalized) {
			continue
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

func targetSearchHasCurrentLevel(req TargetTreeSearchRequest) bool {
	return strings.TrimSpace(string(req.TargetType)) != "" &&
		(strings.TrimSpace(req.TargetRef) != "" || strings.TrimSpace(req.NodeRef) != "")
}

func isLocalFSTargetSearch(req TargetTreeSearchRequest) bool {
	return strings.EqualFold(strings.TrimSpace(string(req.ConnectorType)), "local_fs")
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
