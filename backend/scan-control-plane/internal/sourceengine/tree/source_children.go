package tree

import (
	"context"
	"strings"

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

func shouldExpandBindingRoot(req SourceTreeChildrenRequest, binding store.Binding, parentKey string) bool {
	return strings.TrimSpace(req.ParentKey) == "" &&
		strings.TrimSpace(req.NodeRef) == "" &&
		strings.TrimSpace(req.ParentRef) == "" &&
		strings.TrimSpace(req.Key) == "" &&
		parentKey == binding.TreeKey &&
		binding.TreeKey != ""
}
