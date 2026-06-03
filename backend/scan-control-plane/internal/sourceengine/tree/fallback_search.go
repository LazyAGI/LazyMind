package tree

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type IndexedTargetTreeFallbackSearch struct {
	repo   SourceTreeReadRepository
	limits TreeQueryLimits
}

func NewIndexedTargetTreeFallbackSearch(repo SourceTreeReadRepository, limits TreeQueryLimits) *IndexedTargetTreeFallbackSearch {
	return &IndexedTargetTreeFallbackSearch{repo: repo, limits: defaultLimits(limits)}
}

func (s *IndexedTargetTreeFallbackSearch) Search(ctx context.Context, req TargetTreeSearchRequest) (TreeNodePage, error) {
	if strings.TrimSpace(req.Keyword) == "" {
		return TreeNodePage{}, NewError(ErrCodeInvalidRequest, "keyword is required")
	}
	if strings.TrimSpace(req.TargetRef) == "" {
		return TreeNodePage{SearchMode: SearchModeFallback}, nil
	}
	pageSize := normalizePageSize(req.PageSize, s.limits)
	items, nextCursor, hasMore, err := s.repo.SearchObjects(ctx, ObjectSearchRequest{
		BindingID:         scopedFallbackBindingID(req),
		TreeKey:           scopedFallbackTreeKey(req),
		Keyword:           req.Keyword,
		IncludeDocuments:  true,
		IncludeContainers: true,
		PageSize:          pageSize,
		Cursor:            req.Cursor,
	})
	if err != nil {
		return TreeNodePage{}, mapStoreError(err)
	}
	page := objectPage(items, nextCursor, hasMore, false)
	page.SearchMode = SearchModeFallback
	return page, nil
}

func scopedFallbackBindingID(req TargetTreeSearchRequest) string {
	for key, value := range req.ProviderOptions {
		if key == "binding_id" {
			if text := strings.TrimSpace(stringProviderOption(value)); text != "" {
				return text
			}
		}
	}
	return req.TargetRef
}

func scopedFallbackTreeKey(req TargetTreeSearchRequest) string {
	for _, key := range []string{"tree_key", "root_object_key"} {
		if value := strings.TrimSpace(connector.ProviderOptions(req.ProviderOptions).String(key)); value != "" {
			return value
		}
	}
	return ""
}

func stringProviderOption(value any) string {
	text, _ := value.(string)
	return text
}
