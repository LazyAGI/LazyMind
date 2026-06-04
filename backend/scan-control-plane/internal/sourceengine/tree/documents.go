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
	summary, err := q.repo.GetSourceSummary(ctx, store.SourceSummaryRequest{SourceID: req.SourceID, BindingID: req.BindingID})
	if err != nil {
		return SourceDocumentListResponse{}, mapStoreError(err)
	}
	return SourceDocumentListResponse{Items: items, Total: total, Page: req.Page, PageSize: req.PageSize, Summary: documentSummaryMap(summary)}, nil
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

func documentSummaryMap(summary store.SourceSummary) map[string]any {
	return map[string]any{
		"source_id":             summary.SourceID,
		"binding_id":            summary.BindingID,
		"total_objects":         summary.TotalObjects,
		"document_objects":      summary.DocumentObjects,
		"container_objects":     summary.ContainerObjects,
		"new_count":             summary.NewCount,
		"modified_count":        summary.ModifiedCount,
		"deleted_count":         summary.DeletedCount,
		"unchanged_count":       summary.UnchangedCount,
		"pending_task_count":    summary.PendingTaskCount,
		"running_task_count":    summary.RunningTaskCount,
		"submitted_task_count":  summary.SubmittedTaskCount,
		"failed_task_count":     summary.FailedTaskCount,
		"succeeded_task_count":  summary.SucceededTaskCount,
		"superseded_task_count": summary.SupersededTaskCount,
		"storage_bytes":         summary.StorageBytes,
		"total_document_count":  summary.DocumentObjects,
		"parsed_document_count": summary.SucceededTaskCount,
		"pending_pull_count":    summary.PendingTaskCount + summary.RunningTaskCount + summary.SubmittedTaskCount,
	}
}
