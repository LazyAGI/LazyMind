package admin

import (
	"context"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/observability"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type Store interface {
	ListDeletingResources(ctx context.Context, req store.AdminListRequest) ([]store.DeletingResource, int, error)
	ListFailedCreateOperations(ctx context.Context, req store.CreateOperationListRequest) ([]store.CreateOperation, int, error)
	ClaimCreateOperationCompensation(ctx context.Context, operationID string) (store.CreateOperation, error)
	UpdateCreateOperationByID(ctx context.Context, operation store.CreateOperation) error
	ListDeadLetters(ctx context.Context, req store.DeadLetterListRequest) ([]store.ParseTaskDeadLetter, int, error)
	GetDeadLetter(ctx context.Context, deadLetterID string) (store.ParseTaskDeadLetter, error)
	DeleteDeadLetter(ctx context.Context, deadLetterID string) error
	EnqueueBindingReconcile(ctx context.Context, req store.ReconcileRequest) (store.ReconcileResult, error)
}

type Service struct {
	store   Store
	tasks   taskengine.Planner
	core    coreclient.ResourceClient
	metrics *observability.Registry
	logger  *observability.Logger
	clock   func() time.Time
}

func NewService(store Store, tasks taskengine.Planner, core coreclient.ResourceClient, metrics *observability.Registry, logger *observability.Logger) *Service {
	return &Service{
		store:   store,
		tasks:   tasks,
		core:    core,
		metrics: metrics,
		logger:  logger,
		clock:   time.Now,
	}
}

func (s *Service) ListDeletingResources(ctx context.Context, req ListRequest) (DeletingResourceListResponse, error) {
	items, total, err := s.store.ListDeletingResources(ctx, store.AdminListRequest{
		SourceIDs: req.SourceIDs,
		SourceID:  req.SourceID,
		BindingID: req.BindingID,
		Page:      req.Page,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return DeletingResourceListResponse{}, err
	}
	resp := DeletingResourceListResponse{Items: make([]DeletingResourceResponse, 0, len(items)), Total: total}
	for _, item := range items {
		resp.Items = append(resp.Items, deletingResourceResponse(item))
	}
	return resp, nil
}

func (s *Service) ListCompensations(ctx context.Context, req ListRequest) (CompensationListResponse, error) {
	items, total, err := s.store.ListFailedCreateOperations(ctx, store.CreateOperationListRequest{
		SourceIDs:            req.SourceIDs,
		Statuses:             []string{"FAILED"},
		CompensationStatuses: []string{"FAILED"},
		Page:                 req.Page,
		PageSize:             req.PageSize,
	})
	if err != nil {
		return CompensationListResponse{}, err
	}
	resp := CompensationListResponse{Items: make([]CompensationResponse, 0, len(items)), Total: total}
	for _, item := range items {
		resp.Items = append(resp.Items, compensationResponse(item))
	}
	return resp, nil
}

func (s *Service) RetryCompensation(ctx context.Context, actorID, operationID string) (CompensationResponse, error) {
	op, err := s.store.ClaimCreateOperationCompensation(ctx, operationID)
	if err != nil {
		s.metricCompensation("create_operation", "failed")
		return CompensationResponse{}, err
	}
	errs := s.retryCreateCompensation(ctx, op)
	if len(errs) == 0 {
		op.CompensationStatus = "SUCCEEDED"
		op.CompensationError = nil
		s.metricCompensation("create_operation", "succeeded")
	} else {
		op.CompensationStatus = "FAILED"
		op.CompensationError = store.JSON{"items": errs}
		s.metricCompensation("create_operation", "failed")
	}
	if err := s.store.UpdateCreateOperationByID(ctx, op); err != nil {
		return CompensationResponse{}, err
	}
	s.audit(ctx, "compensation_retry", map[string]any{
		"operation_id": op.OperationID,
		"source_id":    op.SourceID,
		"actor_id":     actorID,
	})
	return compensationResponse(op), nil
}

func (s *Service) retryCreateCompensation(ctx context.Context, op store.CreateOperation) []map[string]any {
	if s.core == nil {
		return []map[string]any{{"code": "INTERNAL_ERROR", "message": "core resource client is not configured"}}
	}
	var errs []map[string]any
	folderIDs := jsonItems(op.CreatedCoreParentDocumentIDs)
	for i := len(folderIDs) - 1; i >= 0; i-- {
		if err := s.core.DeleteDocument(ctx, coreclient.DeleteDocumentRequest{DatasetID: op.DatasetID, DocumentID: folderIDs[i], UserID: op.CallerID}); err != nil {
			errs = append(errs, map[string]any{"code": "CORE_DELETE_FAILED", "message": err.Error(), "core_parent_document_id": folderIDs[i]})
		}
	}
	if op.DatasetID != "" {
		deleter, ok := s.core.(coreclient.DatasetDeletionClient)
		if !ok {
			errs = append(errs, map[string]any{"code": "CORE_DELETE_FAILED", "message": "core client does not support dataset deletion", "dataset_id": op.DatasetID})
		} else if err := deleter.DeleteDataset(ctx, coreclient.DeleteDatasetRequest{DatasetID: op.DatasetID, UserID: op.CallerID}); err != nil {
			errs = append(errs, map[string]any{"code": "CORE_DELETE_FAILED", "message": err.Error(), "dataset_id": op.DatasetID})
		}
	}
	return errs
}

func (s *Service) ListDeadLetters(ctx context.Context, req ListRequest) (DeadLetterListResponse, error) {
	items, total, err := s.store.ListDeadLetters(ctx, store.DeadLetterListRequest{
		SourceIDs: req.SourceIDs,
		SourceID:  req.SourceID,
		BindingID: req.BindingID,
		Page:      req.Page,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return DeadLetterListResponse{}, err
	}
	resp := DeadLetterListResponse{Items: make([]DeadLetterResponse, 0, len(items)), Total: total}
	for _, item := range items {
		resp.Items = append(resp.Items, deadLetterResponse(item))
	}
	return resp, nil
}

func (s *Service) RetryDeadLetter(ctx context.Context, actorID, deadLetterID string, force bool) (taskengine.ParseTaskDetailResponse, error) {
	letter, err := s.store.GetDeadLetter(ctx, deadLetterID)
	if err != nil {
		return taskengine.ParseTaskDetailResponse{}, err
	}
	if s.tasks == nil {
		return taskengine.ParseTaskDetailResponse{}, taskengine.NewError(taskengine.ErrCodeInternal, "task planner is not configured")
	}
	resp, err := s.tasks.RetryTask(ctx, taskengine.RetryRequest{
		CallerID: actorID,
		TenantID: letter.TenantID,
		TaskID:   letter.TaskID,
		Force:    force,
	})
	if err != nil {
		return taskengine.ParseTaskDetailResponse{}, err
	}
	if err := s.store.DeleteDeadLetter(ctx, deadLetterID); err != nil {
		return taskengine.ParseTaskDetailResponse{}, err
	}
	s.metricParseTask(letter.TaskAction, resp.Task.Status)
	s.audit(ctx, "dead_letter_retry", map[string]any{
		"dead_letter_id":     deadLetterID,
		"task_id":            letter.TaskID,
		"source_id":          letter.SourceID,
		"binding_id":         letter.BindingID,
		"binding_generation": letter.BindingGeneration,
		"object_key":         letter.ObjectKey,
		"actor_id":           actorID,
	})
	return resp, nil
}

func (s *Service) ReconcileBinding(ctx context.Context, actorID, sourceID, bindingID, requestID string) (ReconcileResponse, error) {
	result, err := s.store.EnqueueBindingReconcile(ctx, store.ReconcileRequest{
		SourceID:  sourceID,
		BindingID: bindingID,
		RequestID: requestID,
		RunAt:     s.clock().UTC(),
	})
	if err != nil {
		return ReconcileResponse{}, err
	}
	s.metricSyncRun(result.Run.ScopeType, result.Run.Status)
	s.audit(ctx, "binding_reconcile", map[string]any{
		"run_id":             result.Run.RunID,
		"source_id":          result.Run.SourceID,
		"binding_id":         result.Run.BindingID,
		"binding_generation": result.Run.BindingGeneration,
		"actor_id":           actorID,
	})
	return ReconcileResponse{
		RunID:             result.Run.RunID,
		SourceID:          result.Run.SourceID,
		BindingID:         result.Run.BindingID,
		BindingGeneration: result.Run.BindingGeneration,
		Status:            result.Run.Status,
		TriggerType:       result.Run.TriggerType,
		ScopeType:         result.Run.ScopeType,
	}, nil
}

func (s *Service) metricCompensation(resourceType, status string) {
	if s.metrics != nil {
		s.metrics.Inc("sourceengine_compensation_total", observability.Labels{"resource_type": resourceType, "status": status})
	}
}

func (s *Service) metricParseTask(action, status string) {
	if s.metrics != nil {
		s.metrics.Inc("sourceengine_parse_tasks_total", observability.Labels{"connector_type": "unknown", "task_action": action, "status": status})
	}
}

func (s *Service) metricSyncRun(scopeType, status string) {
	if s.metrics != nil {
		s.metrics.Inc("sourceengine_sync_runs_total", observability.Labels{"connector_type": "unknown", "scope_type": scopeType, "status": status})
	}
}

func (s *Service) audit(ctx context.Context, event string, fields map[string]any) {
	if s.logger != nil {
		s.logger.Info(ctx, "scan-control-plane audit", observability.AuditFields(event, fields))
	}
}

func deletingResourceResponse(item store.DeletingResource) DeletingResourceResponse {
	return DeletingResourceResponse{
		ResourceType: item.ResourceType,
		SourceID:     item.SourceID,
		BindingID:    item.BindingID,
		Status:       item.Status,
		DeletedAt:    item.DeletedAt,
		LastError:    store.CloneJSON(item.LastError),
		UpdatedAt:    item.UpdatedAt,
	}
}

func compensationResponse(op store.CreateOperation) CompensationResponse {
	return CompensationResponse{
		OperationID:        op.OperationID,
		SourceID:           op.SourceID,
		DatasetID:          op.DatasetID,
		Status:             op.Status,
		CompensationStatus: op.CompensationStatus,
		CompensationError:  store.CloneJSON(op.CompensationError),
		UpdatedAt:          op.UpdatedAt,
	}
}

func deadLetterResponse(item store.ParseTaskDeadLetter) DeadLetterResponse {
	return DeadLetterResponse{
		DeadLetterID:      item.DeadLetterID,
		TaskID:            item.TaskID,
		SourceID:          item.SourceID,
		BindingID:         item.BindingID,
		BindingGeneration: item.BindingGeneration,
		ObjectKey:         item.ObjectKey,
		DocumentID:        item.DocumentID,
		TaskAction:        item.TaskAction,
		TargetVersionID:   item.TargetVersionID,
		RetryCount:        item.RetryCount,
		ErrorCode:         item.ErrorCode,
		LastError:         store.CloneJSON(item.LastError),
		FailedAt:          item.FailedAt,
		CreatedAt:         item.CreatedAt,
	}
}

func jsonItems(in store.JSON) []string {
	if in == nil {
		return nil
	}
	raw, ok := in["items"]
	if !ok {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
