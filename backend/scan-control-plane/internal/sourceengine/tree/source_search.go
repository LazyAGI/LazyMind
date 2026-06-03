package tree

import (
	"context"
	"strings"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DBSourceTreeQueryEngine) Search(ctx context.Context, req SourceTreeSearchRequest) (TreeNodePage, error) {
	if strings.TrimSpace(req.Keyword) == "" {
		return TreeNodePage{}, NewError(ErrCodeInvalidRequest, "keyword is required")
	}
	req = defaultSourceTreeSearchIncludes(req)
	if _, err := e.repo.GetSource(ctx, req.SourceID); err != nil {
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
	page := objectPage(items, nextCursor, hasMore, false)
	page.SearchMode = "indexed"
	return page, nil
}

func defaultSourceTreeSearchIncludes(req SourceTreeSearchRequest) SourceTreeSearchRequest {
	if !req.IncludeDocuments && !req.IncludeContainers {
		req.IncludeDocuments = true
		req.IncludeContainers = true
	}
	return req
}
