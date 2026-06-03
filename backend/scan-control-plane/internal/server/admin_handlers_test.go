package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminservice "github.com/lazymind/scan_control_plane/internal/admin"
	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/observability"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestAdminHandlersExposeDeletingDeadLettersReconcileAndMetrics(t *testing.T) {
	t.Parallel()

	metrics := observability.NewRegistry()
	metrics.Inc("sourceengine_compensation_total", observability.Labels{"resource_type": "binding", "status": "failed"})
	adminStore := &adminStoreStub{}
	tasks := &adminTaskPlannerStub{}
	adminSvc := adminservice.NewService(adminStore, tasks, adminCoreStub{}, metrics, observability.NewLogger(nil))
	handler := NewHandler(
		WithAdminService(adminSvc),
		WithMetricsRegistry(metrics),
		WithAccessChecker(allowAccess{}),
	)

	deletingReq := httptest.NewRequest(http.MethodGet, "/api/scan/admin/deleting?source_id=source-1&page=2&page_size=5", nil)
	setAPIContractActor(deletingReq)
	deletingResp := httptest.NewRecorder()
	handler.ServeHTTP(deletingResp, deletingReq)
	if deletingResp.Code != http.StatusOK || adminStore.lastDeleting.SourceID != "source-1" || adminStore.lastDeleting.Page != 2 {
		t.Fatalf("unexpected deleting response: code=%d req=%+v body=%s", deletingResp.Code, adminStore.lastDeleting, deletingResp.Body.String())
	}
	if !strings.Contains(deletingResp.Body.String(), `"resource_type":"binding"`) {
		t.Fatalf("deleting response missing binding item: %s", deletingResp.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/scan/admin/dead-letters/dead-letter-task-1/retry", strings.NewReader(`{"force":true}`))
	setAPIContractActor(retryReq)
	retryResp := httptest.NewRecorder()
	handler.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusOK || tasks.lastRetry.TaskID != "task-1" || !tasks.lastRetry.Force || !adminStore.deadLetterDeleted {
		t.Fatalf("dead letter retry did not use planner and clear dead letter: code=%d retry=%+v deleted=%v body=%s", retryResp.Code, tasks.lastRetry, adminStore.deadLetterDeleted, retryResp.Body.String())
	}

	reconcileReq := httptest.NewRequest(http.MethodPost, "/api/scan/admin/sources/source-1/bindings/binding-1/reconcile", strings.NewReader(`{"request_id":"ops-1"}`))
	setAPIContractActor(reconcileReq)
	reconcileResp := httptest.NewRecorder()
	handler.ServeHTTP(reconcileResp, reconcileReq)
	if reconcileResp.Code != http.StatusOK || adminStore.lastReconcile.RequestID != "ops-1" {
		t.Fatalf("unexpected reconcile response: code=%d req=%+v body=%s", reconcileResp.Code, adminStore.lastReconcile, reconcileResp.Body.String())
	}

	metricsResp := httptest.NewRecorder()
	handler.ServeHTTP(metricsResp, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsResp.Code != http.StatusOK || !strings.Contains(metricsResp.Body.String(), "sourceengine_compensation_total") {
		t.Fatalf("metrics endpoint missing counter: code=%d body=%s", metricsResp.Code, metricsResp.Body.String())
	}
}

type adminStoreStub struct {
	lastDeleting      store.AdminListRequest
	lastReconcile     store.ReconcileRequest
	deadLetterDeleted bool
}

func (s *adminStoreStub) ListDeletingResources(_ context.Context, req store.AdminListRequest) ([]store.DeletingResource, int, error) {
	s.lastDeleting = req
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	return []store.DeletingResource{{
		ResourceType: "binding",
		SourceID:     "source-1",
		BindingID:    "binding-1",
		Status:       "DELETING",
		UpdatedAt:    now,
	}}, 1, nil
}

func (s *adminStoreStub) ListFailedCreateOperations(context.Context, store.CreateOperationListRequest) ([]store.CreateOperation, int, error) {
	return nil, 0, nil
}

func (s *adminStoreStub) ClaimCreateOperationCompensation(context.Context, string) (store.CreateOperation, error) {
	return store.CreateOperation{}, nil
}

func (s *adminStoreStub) UpdateCreateOperationByID(context.Context, store.CreateOperation) error {
	return nil
}

func (s *adminStoreStub) ListDeadLetters(context.Context, store.DeadLetterListRequest) ([]store.ParseTaskDeadLetter, int, error) {
	return nil, 0, nil
}

func (s *adminStoreStub) GetDeadLetter(context.Context, string) (store.ParseTaskDeadLetter, error) {
	return store.ParseTaskDeadLetter{
		DeadLetterID:      "dead-letter-task-1",
		TaskID:            "task-1",
		TenantID:          "tenant-1",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ObjectKey:         "doc-1",
		DocumentID:        "document-1",
		TaskAction:        "CREATE",
		TargetVersionID:   "v1",
		RetryCount:        3,
		FailedAt:          time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC),
	}, nil
}

func (s *adminStoreStub) DeleteDeadLetter(context.Context, string) error {
	s.deadLetterDeleted = true
	return nil
}

func (s *adminStoreStub) EnqueueBindingReconcile(_ context.Context, req store.ReconcileRequest) (store.ReconcileResult, error) {
	s.lastReconcile = req
	return store.ReconcileResult{Run: store.SyncRun{
		RunID:             "run-1",
		SourceID:          req.SourceID,
		BindingID:         req.BindingID,
		BindingGeneration: 1,
		Status:            "PENDING",
		TriggerType:       "reconcile",
		ScopeType:         "full",
	}}, nil
}

type adminTaskPlannerStub struct {
	lastRetry taskengine.RetryRequest
}

func (s *adminTaskPlannerStub) GenerateTasks(context.Context, taskengine.GenerateRequest) (taskengine.GenerateResult, error) {
	return taskengine.GenerateResult{}, nil
}

func (s *adminTaskPlannerStub) GeneratePendingTasks(context.Context, taskengine.GeneratePendingRequest) (taskengine.GenerateResult, error) {
	return taskengine.GenerateResult{}, nil
}

func (s *adminTaskPlannerStub) ExpediteTasks(context.Context, taskengine.ExpediteRequest) (taskengine.ExpediteResult, error) {
	return taskengine.ExpediteResult{}, nil
}

func (s *adminTaskPlannerStub) RetryTask(_ context.Context, req taskengine.RetryRequest) (taskengine.ParseTaskDetailResponse, error) {
	s.lastRetry = req
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	return taskengine.ParseTaskDetailResponse{Task: taskengine.ParseTaskResponse{
		TaskID:            req.TaskID,
		SourceID:          "source-1",
		BindingID:         "binding-1",
		ObjectKey:         "doc-1",
		DocumentID:        "document-1",
		TaskAction:        "CREATE",
		TargetVersionID:   "v1",
		BindingGeneration: 1,
		Status:            "PENDING",
		NextRunAt:         now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}}, nil
}

var _ adminservice.Store = (*adminStoreStub)(nil)
var _ taskengine.Planner = (*adminTaskPlannerStub)(nil)

type adminCoreStub struct{}

func (adminCoreStub) CreateDataset(context.Context, coreclient.CreateDatasetRequest) (coreclient.CreateDatasetResponse, error) {
	return coreclient.CreateDatasetResponse{}, nil
}

func (adminCoreStub) CreateBindingRootDocument(context.Context, coreclient.CreateBindingRootDocumentRequest) (coreclient.CreateBindingRootDocumentResponse, error) {
	return coreclient.CreateBindingRootDocumentResponse{}, nil
}

func (adminCoreStub) DeleteDocument(context.Context, coreclient.DeleteDocumentRequest) error {
	return nil
}

func (adminCoreStub) BatchDeleteDocuments(context.Context, coreclient.BatchDeleteDocumentsRequest) error {
	return nil
}

func (adminCoreStub) DeleteDataset(context.Context, coreclient.DeleteDatasetRequest) error {
	return nil
}
