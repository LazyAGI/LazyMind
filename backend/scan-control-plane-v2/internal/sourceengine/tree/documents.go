package tree

import (
	"context"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type DBSourceDocumentQuery struct {
	repo   SourceTreeReadRepository
	limits TreeQueryLimits
}

func NewDBSourceDocumentQuery(repo SourceTreeReadRepository, limits TreeQueryLimits) *DBSourceDocumentQuery {
	return &DBSourceDocumentQuery{repo: repo, limits: defaultLimits(limits)}
}

func (q *DBSourceDocumentQuery) ListDocuments(ctx context.Context, req SourceDocumentListRequest) (SourceDocumentListResponse, error) {
	if _, err := q.repo.GetSource(ctx, req.SourceID); err != nil {
		return SourceDocumentListResponse{}, mapStoreError(err)
	}
	if req.BindingID != "" {
		if _, err := q.repo.GetBinding(ctx, req.SourceID, req.BindingID); err != nil {
			return SourceDocumentListResponse{}, mapStoreError(err)
		}
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	req.PageSize = normalizePageSize(req.PageSize, q.limits)
	rows, total, err := q.repo.ListDocuments(ctx, storeSourceDocumentListRequest(req))
	if err != nil {
		return SourceDocumentListResponse{}, mapStoreError(err)
	}
	items := make([]SourceDocumentItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, documentItem(row))
	}
	return SourceDocumentListResponse{Items: items, Total: total, Page: req.Page, PageSize: req.PageSize}, nil
}

func storeSourceDocumentListRequest(req SourceDocumentListRequest) store.SourceDocumentListRequest {
	return store.SourceDocumentListRequest{
		SourceID:      req.SourceID,
		BindingID:     req.BindingID,
		Keyword:       req.Keyword,
		StateFilter:   req.StateFilter,
		ParseStatuses: req.ParseStatuses,
		Page:          req.Page,
		PageSize:      req.PageSize,
	}
}
