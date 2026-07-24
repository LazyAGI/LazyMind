package admin

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestRetryCompensationDeletesFoldersThenDatasetAndUpdatesOperation(t *testing.T) {
	t.Parallel()

	adminStore := &serviceStoreStub{
		operation: store.CreateOperation{
			OperationID:                  "op-1",
			SourceID:                     "source-1",
			DatasetID:                    "dataset-1",
			Status:                       "FAILED",
			CompensationStatus:           "FAILED",
			CreatedCoreParentDocumentIDs: store.JSON{"items": []any{"folder-1", "folder-2"}},
			UpdatedAt:                    time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC),
		},
	}
	core := &serviceCoreStub{}
	service := NewService(adminStore, nil, core, nil, nil)

	resp, err := service.RetryCompensation(context.Background(), "ops-user", "op-1")
	if err != nil {
		t.Fatalf("retry compensation: %v", err)
	}
	if resp.CompensationStatus != "SUCCEEDED" || adminStore.updated.CompensationStatus != "SUCCEEDED" {
		t.Fatalf("expected succeeded operation, resp=%+v updated=%+v", resp, adminStore.updated)
	}
	if got, want := core.deletedDocuments, []string{"folder-2", "folder-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted folders = %+v, want %+v", got, want)
	}
	if core.deletedDataset != "dataset-1" {
		t.Fatalf("deleted dataset = %q", core.deletedDataset)
	}
}

type serviceStoreStub struct {
	operation store.CreateOperation
	updated   store.CreateOperation
}

func (s *serviceStoreStub) ListDeletingResources(context.Context, store.AdminListRequest) ([]store.DeletingResource, int, error) {
	return nil, 0, nil
}

func (s *serviceStoreStub) ListFailedCreateOperations(context.Context, store.CreateOperationListRequest) ([]store.CreateOperation, int, error) {
	return nil, 0, nil
}

func (s *serviceStoreStub) ClaimCreateOperationCompensation(context.Context, string) (store.CreateOperation, error) {
	s.operation.CompensationStatus = "RUNNING"
	return s.operation, nil
}

func (s *serviceStoreStub) UpdateCreateOperationByID(_ context.Context, operation store.CreateOperation) error {
	s.updated = operation
	return nil
}

func (s *serviceStoreStub) ListDeadLetters(context.Context, store.DeadLetterListRequest) ([]store.ParseTaskDeadLetter, int, error) {
	return nil, 0, nil
}

func (s *serviceStoreStub) GetDeadLetter(context.Context, string) (store.ParseTaskDeadLetter, error) {
	return store.ParseTaskDeadLetter{}, nil
}

func (s *serviceStoreStub) DeleteDeadLetter(context.Context, string) error {
	return nil
}

func (s *serviceStoreStub) EnqueueBindingReconcile(context.Context, store.ReconcileRequest) (store.ReconcileResult, error) {
	return store.ReconcileResult{}, nil
}

type serviceCoreStub struct {
	deletedDocuments []string
	deletedDataset   string
}

func (s *serviceCoreStub) CreateDataset(context.Context, coreclient.CreateDatasetRequest) (coreclient.CreateDatasetResponse, error) {
	return coreclient.CreateDatasetResponse{}, nil
}

func (s *serviceCoreStub) CreateBindingRootDocument(context.Context, coreclient.CreateBindingRootDocumentRequest) (coreclient.CreateBindingRootDocumentResponse, error) {
	return coreclient.CreateBindingRootDocumentResponse{}, nil
}

func (s *serviceCoreStub) DeleteDocument(_ context.Context, req coreclient.DeleteDocumentRequest) error {
	s.deletedDocuments = append(s.deletedDocuments, req.DocumentID)
	return nil
}

func (s *serviceCoreStub) BatchDeleteDocuments(context.Context, coreclient.BatchDeleteDocumentsRequest) error {
	return nil
}

func (s *serviceCoreStub) DeleteDataset(_ context.Context, req coreclient.DeleteDatasetRequest) error {
	s.deletedDataset = req.DatasetID
	return nil
}

func (s *serviceCoreStub) UpdateDataset(context.Context, coreclient.UpdateDatasetRequest) error {
	return nil
}
