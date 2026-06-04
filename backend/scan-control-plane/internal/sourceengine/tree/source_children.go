package tree

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DBSourceTreeQueryEngine) ListChildren(ctx context.Context, req SourceTreeChildrenRequest) (TreeNodePage, error) {
	if _, err := e.repo.GetSource(ctx, req.SourceID); err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	req = defaultSourceTreeIncludes(req)
	listMode, err := validateListMode(req.ListMode, req.Cursor, req.MaxItems, e.limits)
	if err != nil {
		return TreeNodePage{}, err
	}
	if req.BindingID == "" {
		return e.listBindingRoots(ctx, req.SourceID)
	}
	binding, err := e.repo.GetBinding(ctx, req.SourceID, req.BindingID)
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	if !req.UseCache {
		return e.listLiveChildren(ctx, req, binding, listMode)
	}
	treeKey := req.TreeKey
	if treeKey == "" {
		treeKey = binding.TreeKey
	}
	parentKey := effectiveSourceParentKey(req, binding)
	pageSize := normalizePageSize(req.PageSize, e.limits)
	if listMode == ListModeAllCurrentLevel {
		pageSize = req.MaxItems + 1
	}
	items, nextCursor, hasMore, err := e.listObjects(ctx, req, treeKey, parentKey, pageSize)
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	if shouldExpandBindingRoot(req, binding, parentKey) && len(items) == 0 && !hasMore {
		items, nextCursor, hasMore, err = e.listObjects(ctx, req, treeKey, "", pageSize)
		if err != nil {
			return TreeNodePage{}, mapStoreError(err)
		}
	}
	if listMode == ListModeAllCurrentLevel && len(items) > req.MaxItems {
		return TreeNodePage{}, NewError(ErrCodeResultTooLarge, "current level has more items than max_items")
	}
	return objectPage(items, nextCursor, hasMore, listMode == ListModeAllCurrentLevel), nil
}

func (e *DBSourceTreeQueryEngine) listLiveChildren(ctx context.Context, req SourceTreeChildrenRequest, binding store.Binding, listMode string) (TreeNodePage, error) {
	if e.registry == nil {
		return TreeNodePage{}, NewError(ErrCodeInternal, "source tree connector registry is not configured")
	}
	conn, err := e.registry.Get(connector.ConnectorType(binding.ConnectorType))
	if err != nil {
		return TreeNodePage{}, mapConnectorError(err)
	}
	pageSize := normalizePageSize(req.PageSize, sourceLiveLimits(e.limits, conn.Spec()))
	rawPage, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType:       connector.TargetType(binding.TargetType),
		TargetRef:        binding.TargetRef,
		NodeRef:          liveSourceNodeRef(req, binding),
		ListMode:         connector.ListMode(listMode),
		Cursor:           req.Cursor,
		PageSize:         pageSize,
		MaxItems:         req.MaxItems,
		AgentID:          binding.AgentID,
		AuthConnectionID: binding.AuthConnectionID,
		ProviderOptions:  liveSourceProviderOptions(binding.ProviderOptions, req.ProviderOptions),
	})
	if err != nil {
		return TreeNodePage{}, mapConnectorError(err)
	}
	nodes := make([]TreeNode, 0, len(rawPage.Items))
	for _, raw := range rawPage.Items {
		normalized, err := conn.MapObject(ctx, raw)
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		if normalized.IsDocument && !req.IncludeDocuments {
			continue
		}
		if normalized.IsContainer && !req.IncludeContainers {
			continue
		}
		nodes = append(nodes, liveSourceNode(req.SourceID, binding, raw, normalized))
	}
	return TreeNodePage{
		Items:        nodes,
		NextCursor:   rawPage.NextCursor,
		HasMore:      rawPage.HasMore,
		ListComplete: rawPage.ListComplete,
		SearchMode:   SearchModeConnector,
	}, nil
}

func (e *DBSourceTreeQueryEngine) listObjects(ctx context.Context, req SourceTreeChildrenRequest, treeKey, parentKey string, pageSize int) ([]ObjectWithState, string, bool, error) {
	return e.repo.ListObjects(ctx, store.ObjectListRequest{
		SourceID:          req.SourceID,
		BindingID:         req.BindingID,
		TreeKey:           treeKey,
		ParentKey:         parentKey,
		IncludeDocuments:  req.IncludeDocuments,
		IncludeContainers: req.IncludeContainers,
		StateFilter:       req.StateFilter,
		PageSize:          pageSize,
		Cursor:            req.Cursor,
	})
}

func (e *DBSourceTreeQueryEngine) listBindingRoots(ctx context.Context, sourceID string) (TreeNodePage, error) {
	bindings, err := e.repo.ListBindings(ctx, sourceID)
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	nodes := make([]TreeNode, 0, len(bindings))
	for _, binding := range bindings {
		nodes = append(nodes, bindingRootNode(binding))
	}
	return TreeNodePage{Items: nodes, ListComplete: true}, nil
}

func defaultSourceTreeIncludes(req SourceTreeChildrenRequest) SourceTreeChildrenRequest {
	if !req.IncludeDocuments && !req.IncludeContainers {
		req.IncludeDocuments = true
		req.IncludeContainers = true
	}
	return req
}

func objectPage(items []ObjectWithState, nextCursor string, hasMore bool, listComplete bool) TreeNodePage {
	nodes := make([]TreeNode, 0, len(items))
	for _, item := range items {
		nodes = append(nodes, sourceObjectNode(item))
	}
	return TreeNodePage{Items: nodes, NextCursor: nextCursor, HasMore: hasMore, ListComplete: listComplete}
}

func effectiveSourceParentKey(req SourceTreeChildrenRequest, binding store.Binding) string {
	if parentKey := strings.TrimSpace(req.ParentKey); parentKey != "" {
		return normalizeSourceNodeKey(parentKey, binding)
	}
	for _, ref := range []string{req.NodeRef, req.ParentRef, req.Key} {
		if parentKey := normalizeSourceNodeKey(ref, binding); parentKey != "" {
			return parentKey
		}
	}
	if binding.TreeKey != "" {
		return binding.TreeKey
	}
	return ""
}

func normalizeSourceNodeKey(value string, binding store.Binding) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value == binding.BindingID || value == binding.TargetRef {
		return binding.TreeKey
	}
	if strings.HasPrefix(value, binding.BindingID+":") {
		return strings.TrimPrefix(value, binding.BindingID+":")
	}
	if binding.ConnectorType == "feishu" && (strings.HasPrefix(value, "drive:") || strings.HasPrefix(value, "wiki:")) {
		return "feishu:" + value
	}
	return value
}

func liveSourceNodeRef(req SourceTreeChildrenRequest, binding store.Binding) string {
	for _, ref := range []string{req.NodeRef, req.ParentRef, req.ParentKey, req.Key} {
		if nodeRef := normalizeLiveSourceNodeRef(ref, binding); nodeRef != "" {
			return nodeRef
		}
	}
	return ""
}

func normalizeLiveSourceNodeRef(value string, binding store.Binding) string {
	value = normalizeSourceNodeKey(value, binding)
	if value == "" || value == binding.TreeKey || value == binding.TargetRef {
		return ""
	}
	if binding.ConnectorType == "feishu" && strings.HasPrefix(value, "feishu:") {
		return strings.TrimPrefix(value, "feishu:")
	}
	return value
}

func liveSourceProviderOptions(bindingOptions store.JSON, requestOptions map[string]any) connector.ProviderOptions {
	out := connector.ProviderOptions(store.CloneJSON(bindingOptions))
	if out == nil {
		out = connector.ProviderOptions{}
	}
	for key, value := range requestOptions {
		out[key] = value
	}
	return out
}

func sourceLiveLimits(limits TreeQueryLimits, spec connector.ConnectorSpec) TreeQueryLimits {
	if spec.MaxPageSize > 0 && spec.MaxPageSize < limits.MaxPageSize {
		limits.MaxPageSize = spec.MaxPageSize
	}
	if limits.DefaultPageSize > limits.MaxPageSize {
		limits.DefaultPageSize = limits.MaxPageSize
	}
	return limits
}

func shouldExpandBindingRoot(req SourceTreeChildrenRequest, binding store.Binding, parentKey string) bool {
	return strings.TrimSpace(req.ParentKey) == "" &&
		strings.TrimSpace(req.NodeRef) == "" &&
		strings.TrimSpace(req.ParentRef) == "" &&
		strings.TrimSpace(req.Key) == "" &&
		parentKey == binding.TreeKey &&
		binding.TreeKey != ""
}
