package tree

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/filefilter"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DBSourceTreeQueryEngine) ListChildren(ctx context.Context, req SourceTreeChildrenRequest) (TreeNodePage, error) {
	source, err := e.repo.GetSource(ctx, req.SourceID)
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	req = defaultSourceTreeIncludes(req)
	listMode, err := validateListMode(req.ListMode, req.Cursor, req.MaxItems, e.limits)
	if err != nil {
		return TreeNodePage{}, err
	}
	if req.BindingID == "" {
		return e.listBindingRoots(ctx, source)
	}
	binding, err := e.repo.GetBinding(ctx, req.SourceID, req.BindingID)
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	if sourceTreeRootRequest(req) {
		roots, ok, err := e.maybeListBindingRoots(ctx, source)
		if err != nil {
			return TreeNodePage{}, err
		}
		if ok {
			return roots, nil
		}
	}
	binding, switchedBinding, err := e.resolveBindingForRequestedParent(ctx, req, binding)
	if err != nil {
		return TreeNodePage{}, err
	}
	if switchedBinding {
		req.BindingID = binding.BindingID
	}
	if !sourceTreeUseCache(req) {
		return e.listLiveChildren(ctx, req, source, binding, listMode)
	}
	treeKey := req.TreeKey
	if treeKey == "" || switchedBinding {
		treeKey = binding.TreeKey
	}
	parentKey := effectiveSourceParentKey(req, binding)
	pageSize := normalizePageSize(req.PageSize, e.limits)
	if listMode == ListModeAllCurrentLevel {
		pageSize = req.MaxItems + 1
	}
	if shouldExpandBindingRoot(req, binding, parentKey) {
		root, ok, err := e.indexedBindingRoot(ctx, req, binding)
		if err != nil {
			return TreeNodePage{}, err
		}
		if ok {
			items := filterObjectItems(filefilter.FromSourceBinding(source, binding), []ObjectWithState{root})
			return objectPage(items, "", false, true), nil
		}
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
	items = filterObjectItems(filefilter.FromSourceBinding(source, binding), items)
	return objectPage(items, nextCursor, hasMore, listMode == ListModeAllCurrentLevel), nil
}

func (e *DBSourceTreeQueryEngine) listLiveChildren(ctx context.Context, req SourceTreeChildrenRequest, source store.Source, binding store.Binding, listMode string) (TreeNodePage, error) {
	if e.registry == nil {
		return TreeNodePage{}, NewError(ErrCodeInternal, "source tree connector registry is not configured")
	}
	conn, err := e.registry.Get(connector.ConnectorType(binding.ConnectorType))
	if err != nil {
		return TreeNodePage{}, mapConnectorError(err)
	}
	pageSize := normalizePageSize(req.PageSize, sourceLiveLimits(e.limits, conn.Spec()))
	if shouldFetchLiveBindingRoot(req, binding) {
		rootPage, err := conn.FetchPage(ctx, connector.FetchPageRequest{
			SourceID:          req.SourceID,
			BindingID:         binding.BindingID,
			BindingGeneration: binding.BindingGeneration,
			TargetType:        connector.TargetType(binding.TargetType),
			TargetRef:         binding.TargetRef,
			ScopeType:         connector.ScopeTypeWatchEvent,
			ScopeRef:          connector.ScopeRef{"target_ref": binding.TargetRef},
			PageSize:          1,
			AgentID:           binding.AgentID,
			AuthConnectionID:  binding.AuthConnectionID,
			ProviderOptions:   liveSourceProviderOptions(binding.ProviderOptions, req.ProviderOptions),
		})
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		if len(rootPage.Items) > 0 {
			return e.mapLiveSourcePage(ctx, conn, req, source, binding, connector.RawObjectPage{
				Items:        rootPage.Items[:1],
				ListComplete: true,
			})
		}
	}
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
	return e.mapLiveSourcePage(ctx, conn, req, source, binding, rawPage)
}

func (e *DBSourceTreeQueryEngine) mapLiveSourcePage(ctx context.Context, conn connector.SourceConnector, req SourceTreeChildrenRequest, source store.Source, binding store.Binding, rawPage connector.RawObjectPage) (TreeNodePage, error) {
	nodes := make([]TreeNode, 0, len(rawPage.Items))
	policy := filefilter.FromSourceBinding(source, binding)
	for _, raw := range rawPage.Items {
		normalized, err := conn.MapObject(ctx, raw)
		if err != nil {
			return TreeNodePage{}, mapConnectorError(err)
		}
		if !treeAllowsNormalized(policy, normalized) {
			continue
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

type sourceObjectReader interface {
	GetObject(ctx context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error)
}

type sourceDocumentStateReader interface {
	GetDocumentState(ctx context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error)
}

func (e *DBSourceTreeQueryEngine) indexedBindingRoot(ctx context.Context, req SourceTreeChildrenRequest, binding store.Binding) (ObjectWithState, bool, error) {
	reader, ok := e.repo.(sourceObjectReader)
	if !ok || strings.TrimSpace(binding.TreeKey) == "" {
		return ObjectWithState{}, false, nil
	}
	root, err := reader.GetObject(ctx, req.SourceID, binding.BindingID, binding.TreeKey)
	if err != nil {
		if store.ErrorCodeOf(err) == store.ErrCodeNotFound {
			return ObjectWithState{}, false, nil
		}
		return ObjectWithState{}, false, mapStoreError(err)
	}
	if !root.HasChildren {
		children, _, hasMore, err := e.listObjects(ctx, SourceTreeChildrenRequest{
			SourceID:          req.SourceID,
			BindingID:         binding.BindingID,
			IncludeDocuments:  true,
			IncludeContainers: true,
		}, binding.TreeKey, root.ObjectKey, 1)
		if err != nil {
			return ObjectWithState{}, false, mapStoreError(err)
		}
		root.HasChildren = hasMore || len(children) > 0
	}
	item := ObjectWithState{Object: root}
	if root.IsDocument {
		if stateReader, ok := e.repo.(sourceDocumentStateReader); ok {
			state, err := stateReader.GetDocumentState(ctx, req.SourceID, binding.BindingID, root.ObjectKey)
			if err != nil {
				if store.ErrorCodeOf(err) != store.ErrCodeNotFound {
					return ObjectWithState{}, false, mapStoreError(err)
				}
			} else {
				item.State = &state
			}
		}
	}
	return item, true, nil
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

func (e *DBSourceTreeQueryEngine) listBindingRoots(ctx context.Context, source store.Source) (TreeNodePage, error) {
	bindings, err := e.repo.ListBindings(ctx, source.SourceID)
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	return e.bindingRootsPage(ctx, source, bindings)
}

func (e *DBSourceTreeQueryEngine) bindingRootsPage(ctx context.Context, source store.Source, bindings []store.Binding) (TreeNodePage, error) {
	nodes := make([]TreeNode, 0, len(bindings))
	for _, binding := range bindings {
		node, err := e.bindingRootNode(ctx, source, binding)
		if err != nil {
			return TreeNodePage{}, err
		}
		nodes = append(nodes, node)
	}
	return TreeNodePage{Items: nodes, ListComplete: true}, nil
}

func (e *DBSourceTreeQueryEngine) bindingRootNode(ctx context.Context, source store.Source, binding store.Binding) (TreeNode, error) {
	node := bindingRootNode(binding)
	root, ok, err := e.indexedBindingRoot(ctx, SourceTreeChildrenRequest{
		SourceID:          source.SourceID,
		BindingID:         binding.BindingID,
		IncludeDocuments:  true,
		IncludeContainers: true,
	}, binding)
	if err != nil {
		return TreeNode{}, err
	}
	if ok {
		node = bindingRootNodeWithObject(node, root)
	}
	return node, nil
}

func (e *DBSourceTreeQueryEngine) maybeListBindingRoots(ctx context.Context, source store.Source) (TreeNodePage, bool, error) {
	bindings, err := e.repo.ListBindings(ctx, source.SourceID)
	if err != nil {
		return TreeNodePage{}, false, mapStoreError(err)
	}
	if len(bindings) <= 1 {
		return TreeNodePage{}, false, nil
	}
	page, err := e.bindingRootsPage(ctx, source, bindings)
	if err != nil {
		return TreeNodePage{}, false, err
	}
	return page, true, nil
}

func (e *DBSourceTreeQueryEngine) resolveBindingForRequestedParent(ctx context.Context, req SourceTreeChildrenRequest, fallback store.Binding) (store.Binding, bool, error) {
	refs := requestedParentRefs(req)
	if len(refs) == 0 {
		return fallback, false, nil
	}
	bindings, err := e.repo.ListBindings(ctx, req.SourceID)
	if err != nil {
		return store.Binding{}, false, mapStoreError(err)
	}
	if len(bindings) <= 1 {
		return fallback, false, nil
	}
	reader, hasReader := e.repo.(sourceObjectReader)
	for _, binding := range bindings {
		for _, ref := range refs {
			objectKey := normalizeSourceNodeKey(ref, binding)
			if objectKey == "" {
				continue
			}
			if objectKey == binding.TreeKey {
				return binding, binding.BindingID != fallback.BindingID, nil
			}
			if !hasReader {
				continue
			}
			if _, err := reader.GetObject(ctx, req.SourceID, binding.BindingID, objectKey); err == nil {
				return binding, binding.BindingID != fallback.BindingID, nil
			} else if store.ErrorCodeOf(err) != store.ErrCodeNotFound {
				return store.Binding{}, false, mapStoreError(err)
			}
		}
	}
	return fallback, false, nil
}

func requestedParentRefs(req SourceTreeChildrenRequest) []string {
	refs := make([]string, 0, 4)
	for _, ref := range []string{req.ParentKey, req.NodeRef, req.ParentRef, req.Key} {
		if trimmed := strings.TrimSpace(ref); trimmed != "" {
			refs = append(refs, trimmed)
		}
	}
	return refs
}

func defaultSourceTreeIncludes(req SourceTreeChildrenRequest) SourceTreeChildrenRequest {
	if !req.IncludeDocuments && !req.IncludeContainers {
		req.IncludeDocuments = true
		req.IncludeContainers = true
	}
	return req
}

func sourceTreeUseCache(req SourceTreeChildrenRequest) bool {
	if req.UseCache == nil {
		return true
	}
	return *req.UseCache
}

func sourceTreeRootRequest(req SourceTreeChildrenRequest) bool {
	return strings.TrimSpace(req.ParentKey) == "" &&
		strings.TrimSpace(req.NodeRef) == "" &&
		strings.TrimSpace(req.ParentRef) == "" &&
		strings.TrimSpace(req.Key) == "" &&
		strings.TrimSpace(req.Cursor) == ""
}

func objectPage(items []ObjectWithState, nextCursor string, hasMore bool, listComplete bool) TreeNodePage {
	nodes := make([]TreeNode, 0, len(items))
	for _, item := range items {
		nodes = append(nodes, sourceObjectNode(item))
	}
	return TreeNodePage{Items: nodes, NextCursor: nextCursor, HasMore: hasMore, ListComplete: listComplete}
}

func filterObjectItems(policy filefilter.Policy, items []ObjectWithState) []ObjectWithState {
	out := items[:0]
	for _, item := range items {
		if treeAllowsSourceObject(policy, item.Object) {
			out = append(out, item)
		}
	}
	return out
}

func treeAllowsSourceObject(policy filefilter.Policy, object store.SourceObject) bool {
	return object.IsContainer || object.HasChildren || filefilter.AllowsSourceObject(policy, object)
}

func treeAllowsNormalized(policy filefilter.Policy, object connector.NormalizedSourceObject) bool {
	return object.IsContainer || object.HasChildren || filefilter.AllowsNormalized(policy, object)
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
	for _, ref := range []string{req.NodeRef, req.ParentRef, req.ParentKey, req.Key, req.TreeKey} {
		if nodeRef := normalizeLiveSourceNodeRef(ref, binding); nodeRef != "" {
			return nodeRef
		}
	}
	return ""
}

func normalizeLiveSourceNodeRef(value string, binding store.Binding) string {
	value = normalizeSourceNodeKey(value, binding)
	if binding.ConnectorType == "feishu" && strings.HasPrefix(value, "feishu:wiki:space:") {
		return value
	}
	if binding.ConnectorType == "feishu" && strings.HasPrefix(value, "feishu:") {
		return strings.TrimPrefix(value, "feishu:")
	}
	if value == "" || value == binding.TreeKey || value == binding.TargetRef {
		return ""
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
		strings.TrimSpace(req.Cursor) == "" &&
		parentKey == binding.TreeKey &&
		binding.TreeKey != ""
}

func shouldFetchLiveBindingRoot(req SourceTreeChildrenRequest, binding store.Binding) bool {
	if binding.ConnectorType != "local_fs" && !(binding.ConnectorType == "feishu" && binding.TargetType == "wiki_node") {
		return false
	}
	return shouldExpandBindingRoot(req, binding, effectiveSourceParentKey(req, binding))
}
