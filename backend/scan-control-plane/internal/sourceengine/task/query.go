package task

import (
	"context"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type DBParseTaskQuery struct {
	store QueryStore
}

func NewDBParseTaskQuery(store QueryStore) *DBParseTaskQuery {
	return &DBParseTaskQuery{store: store}
}

func (q *DBParseTaskQuery) ListParseTasks(ctx context.Context, req ParseTaskQueryRequest) (ParseTaskListResponse, error) {
	items, total, err := q.store.ListParseTasks(ctx, storeParseTaskListRequest(req))
	if err != nil {
		return ParseTaskListResponse{}, mapStoreError(err)
	}
	resp := ParseTaskListResponse{Items: make([]ParseTaskResponse, 0, len(items)), Total: total}
	for _, item := range items {
		resp.Items = append(resp.Items, parseTaskResponse(item))
	}
	return resp, nil
}

func (q *DBParseTaskQuery) GetParseTask(ctx context.Context, taskID string) (ParseTaskDetailResponse, error) {
	item, err := q.store.GetParseTask(ctx, taskID)
	if err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	return parseTaskDetailResponse(item), nil
}

func (q *DBParseTaskQuery) GetParseTaskStats(ctx context.Context, req ParseTaskQueryRequest) (ParseTaskStatsResponse, error) {
	stats, err := q.store.GetParseTaskStats(ctx, store.ParseTaskStatsRequest{
		SourceIDs:  req.SourceIDs,
		SourceID:   req.SourceID,
		BindingID:  req.BindingID,
		DocumentID: req.DocumentID,
	})
	if err != nil {
		return ParseTaskStatsResponse{}, mapStoreError(err)
	}
	return ParseTaskStatsResponse{
		Total:                stats.Total,
		ByStatus:             stats.ByStatus,
		ByAction:             stats.ByAction,
		RetryableFailedCount: stats.RetryableFailedCount,
	}, nil
}

func storeParseTaskListRequest(req ParseTaskQueryRequest) store.ParseTaskListRequest {
	return store.ParseTaskListRequest{
		SourceIDs:   req.SourceIDs,
		SourceID:    req.SourceID,
		BindingID:   req.BindingID,
		DocumentID:  req.DocumentID,
		Statuses:    req.Statuses,
		TaskActions: req.TaskActions,
		Page:        req.Page,
		PageSize:    req.PageSize,
	}
}

func parseTaskDetailResponse(item store.ParseTaskWithRefs) ParseTaskDetailResponse {
	resp := ParseTaskDetailResponse{Task: parseTaskResponse(item)}
	if item.Document != nil {
		doc := documentResponse(*item.Document)
		resp.Document = &doc
	}
	if item.State != nil {
		state := documentStateResponse(*item.State)
		resp.State = &state
	}
	if item.Object != nil {
		object := objectResponse(*item.Object)
		resp.Object = &object
	}
	return resp
}

func parseTaskResponse(item store.ParseTaskWithRefs) ParseTaskResponse {
	task := item.Task
	resp := ParseTaskResponse{
		TaskID:               task.TaskID,
		SourceID:             task.SourceID,
		BindingID:            task.BindingID,
		ObjectKey:            task.ObjectKey,
		DocumentID:           task.DocumentID,
		TaskAction:           task.TaskAction,
		TargetVersionID:      task.TargetVersionID,
		SourceVersion:        task.SourceVersion,
		BindingGeneration:    task.BindingGeneration,
		Status:               task.Status,
		CoreTaskID:           task.CoreTaskID,
		CoreDocumentID:       task.CoreDocumentID,
		CoreParentDocumentID: task.CoreParentDocumentID,
		LeaseOwner:           task.LeaseOwner,
		LeaseUntil:           task.LeaseUntil,
		RetryCount:           task.RetryCount,
		NextRunAt:            task.NextRunAt,
		LastError:            store.CloneJSON(task.LastError),
		CreatedAt:            task.CreatedAt,
		UpdatedAt:            task.UpdatedAt,
	}
	if item.Document != nil && item.Document.DisplayName != "" {
		resp.DisplayName = item.Document.DisplayName
	}
	if resp.DisplayName == "" && item.Object != nil {
		resp.DisplayName = item.Object.DisplayName
	}
	return resp
}

func documentResponse(document store.Document) DocumentResponse {
	return DocumentResponse{
		DocumentID:       document.DocumentID,
		SourceID:         document.SourceID,
		BindingID:        document.BindingID,
		ObjectKey:        document.ObjectKey,
		CoreDocumentID:   document.CoreDocumentID,
		CurrentVersionID: document.CurrentVersionID,
		DesiredVersionID: document.DesiredVersionID,
		SourceVersion:    document.SourceVersion,
		DisplayName:      document.DisplayName,
		ParseStatus:      document.ParseStatus,
		CreatedAt:        document.CreatedAt,
		UpdatedAt:        document.UpdatedAt,
	}
}

func documentStateResponse(state store.DocumentState) DocumentStateResponse {
	return DocumentStateResponse{
		SourceID:            state.SourceID,
		BindingID:           state.BindingID,
		BindingGeneration:   state.BindingGeneration,
		ObjectKey:           state.ObjectKey,
		SourceVersion:       state.SourceVersion,
		BaselineVersion:     state.BaselineVersion,
		SourceState:         state.SourceState,
		SyncState:           state.SyncState,
		PendingAction:       state.PendingAction,
		DocumentListVisible: state.DocumentListVisible,
		Selectable:          state.Selectable,
		ParseQueueState:     state.ParseQueueState,
		DocumentID:          state.DocumentID,
		ActiveTaskID:        state.ActiveTaskID,
		LastDetectedAt:      state.LastDetectedAt,
		LastSyncedAt:        state.LastSyncedAt,
		LastError:           store.CloneJSON(state.LastError),
		CreatedAt:           state.CreatedAt,
		UpdatedAt:           state.UpdatedAt,
	}
}

func objectResponse(object store.SourceObject) ObjectResponse {
	return ObjectResponse{
		SourceID:      object.SourceID,
		BindingID:     object.BindingID,
		ObjectKey:     object.ObjectKey,
		DisplayName:   object.DisplayName,
		SourceVersion: object.SourceVersion,
		IsDocument:    object.IsDocument,
		IsContainer:   object.IsContainer,
		CreatedAt:     object.CreatedAt,
		UpdatedAt:     object.UpdatedAt,
	}
}

