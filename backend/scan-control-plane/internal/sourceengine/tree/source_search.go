package tree

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/filefilter"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DBSourceTreeQueryEngine) Search(ctx context.Context, req SourceTreeSearchRequest) (TreeNodePage, error) {
	if strings.TrimSpace(req.Keyword) == "" {
		return TreeNodePage{}, NewError(ErrCodeInvalidRequest, "keyword is required")
	}
	if err := validateSearchListMode(req.ListMode); err != nil {
		return TreeNodePage{}, err
	}
	req = defaultSourceTreeSearchIncludes(req)
	source, err := e.repo.GetSource(ctx, req.SourceID)
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	if req.BindingID != "" {
		if _, err := e.repo.GetBinding(ctx, req.SourceID, req.BindingID); err != nil {
			return TreeNodePage{}, mapStoreError(err)
		}
	}
	pageSize := normalizePageSize(req.PageSize, e.limits)
	items, nextCursor, hasMore, err := e.repo.SearchObjects(ctx, store.ObjectSearchRequest{
		SourceID:          req.SourceID,
		BindingID:         req.BindingID,
		TreeKey:           req.TreeKey,
		Keyword:           req.Keyword,
		IncludeDocuments:  req.IncludeDocuments,
		IncludeContainers: req.IncludeContainers,
		StateFilter:       req.StateFilter,
		PageSize:          pageSize,
		Cursor:            req.Cursor,
	})
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	items, err = e.filterSearchItems(ctx, source, items)
	if err != nil {
		return TreeNodePage{}, err
	}
	page := objectPage(items, nextCursor, hasMore, false)
	page.SearchMode = "indexed"
	return page, nil
}

func (e *DBSourceTreeQueryEngine) filterSearchItems(ctx context.Context, source store.Source, items []ObjectWithState) ([]ObjectWithState, error) {
	policies := map[string]filefilter.Policy{}
	out := items[:0]
	for _, item := range items {
		policy, err := e.policyForObject(ctx, source, item.Object.BindingID, policies)
		if err != nil {
			return nil, err
		}
		if treeAllowsObjectWithState(policy, item) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (e *DBSourceTreeQueryEngine) policyForObject(ctx context.Context, source store.Source, bindingID string, policies map[string]filefilter.Policy) (filefilter.Policy, error) {
	if policy, ok := policies[bindingID]; ok {
		return policy, nil
	}
	binding, err := e.repo.GetBinding(ctx, source.SourceID, bindingID)
	if err != nil {
		return filefilter.Policy{}, mapStoreError(err)
	}
	policy := filefilter.FromSourceBinding(source, binding)
	policies[bindingID] = policy
	return policy, nil
}

func defaultSourceTreeSearchIncludes(req SourceTreeSearchRequest) SourceTreeSearchRequest {
	if !req.IncludeDocuments && !req.IncludeContainers {
		req.IncludeDocuments = true
		req.IncludeContainers = true
	}
	return req
}
