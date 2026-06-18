package tree

import (
	"context"
	"strings"

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
	items, removed := dedupeDocumentItems(items)
	if removed > 0 && total >= removed {
		total -= removed
	}
	summary, err := q.repo.GetSourceSummary(ctx, store.SourceSummaryRequest{SourceID: req.SourceID, BindingID: req.BindingID})
	if err != nil {
		return SourceDocumentListResponse{}, mapStoreError(err)
	}
	return SourceDocumentListResponse{Items: items, Total: total, Page: req.Page, PageSize: req.PageSize, Summary: documentSummaryMap(summary)}, nil
}

func dedupeDocumentItems(items []SourceDocumentItem) ([]SourceDocumentItem, int) {
	if len(items) < 2 {
		return items, 0
	}
	out := make([]SourceDocumentItem, 0, len(items))
	positions := map[string]int{}
	removed := 0
	for _, item := range items {
		key := documentLogicalKey(item)
		if key == "" {
			out = append(out, item)
			continue
		}
		if idx, ok := positions[key]; ok {
			removed++
			if documentItemPreferred(item, out[idx]) {
				out[idx] = item
			}
			continue
		}
		positions[key] = len(out)
		out = append(out, item)
	}
	return out, removed
}

func documentLogicalKey(item SourceDocumentItem) string {
	path := strings.TrimSpace(item.Path)
	if path == "" {
		return ""
	}
	return strings.Join([]string{
		strings.TrimSpace(item.SourceID),
		strings.TrimSpace(item.BindingID),
		path,
	}, "\x00")
}

func documentItemPreferred(candidate, current SourceDocumentItem) bool {
	candidateRank := documentItemDisplayRank(candidate)
	currentRank := documentItemDisplayRank(current)
	if candidateRank != currentRank {
		return candidateRank < currentRank
	}
	if candidate.LastSyncedAt != nil && current.LastSyncedAt != nil {
		return candidate.LastSyncedAt.After(*current.LastSyncedAt)
	}
	if candidate.LastSyncedAt != nil {
		return true
	}
	if current.LastSyncedAt != nil {
		return false
	}
	if candidate.SourceModifiedAt != nil && current.SourceModifiedAt != nil {
		return candidate.SourceModifiedAt.After(*current.SourceModifiedAt)
	}
	return candidate.DocumentID != "" && current.DocumentID == ""
}

func documentItemDisplayRank(item SourceDocumentItem) int {
	parseState := firstNonEmptyString(item.ParseQueueState, item.ParseState, item.ParseStatus)
	if activeParseState(parseState) {
		return 0
	}
	if item.HasUpdate || strings.TrimSpace(item.PendingAction) != "" {
		return 1
	}
	if strings.ToUpper(strings.TrimSpace(parseState)) == store.ParseTaskStatusFailed {
		return 2
	}
	return 3
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
