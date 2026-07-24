package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/compat/contract"
	compatknowledge "lazymind/core/compat/knowledge"
	"lazymind/core/doc"
)

type fakeDatasetCatalogService struct {
	listReq doc.DatasetListRequest
	getReq  doc.DatasetGetRequest
	listRes doc.DatasetListResult
	getRes  doc.Dataset
	listErr error
	getErr  error
}

func (s *fakeDatasetCatalogService) ListDatasets(ctx context.Context, req doc.DatasetListRequest) (doc.DatasetListResult, error) {
	s.listReq = req
	if s.listErr != nil {
		return doc.DatasetListResult{}, s.listErr
	}
	return s.listRes, nil
}

func (s *fakeDatasetCatalogService) GetDataset(ctx context.Context, req doc.DatasetGetRequest) (doc.Dataset, error) {
	s.getReq = req
	if s.getErr != nil {
		return doc.Dataset{}, s.getErr
	}
	return s.getRes, nil
}

func TestKnowledgeAdapterListPassesUserFiltersAndPaging(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	service := &fakeDatasetCatalogService{
		listRes: doc.DatasetListResult{
			Datasets: []doc.Dataset{{
				DatasetID:     "ds-1",
				DisplayName:   "Product Docs",
				Desc:          "API references",
				Tags:          []string{"api", "release"},
				UpdateTime:    now,
				DocumentSize:  42,
				DocumentCount: 3,
			}},
			TotalSize:  12,
			NextOffset: 42,
			HasMore:    true,
		},
	}
	adapter := mustKnowledgeAdapter(t, service)
	result, err := adapter.List(context.Background(), contract.CallContext{UserID: " user-1 "}, compatknowledge.ListInput{
		Keyword: " docs ",
		Tags:    []string{"api"},
		Page:    contract.PageRequest{PageSize: 20, PageToken: contract.EncodeOffsetPageToken(22)},
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if service.listReq.UserID != "user-1" || service.listReq.Caller.UserID != "user-1" {
		t.Fatalf("List user = %q caller=%q, want user-1", service.listReq.UserID, service.listReq.Caller.UserID)
	}
	if service.listReq.Keyword != "docs" || service.listReq.Offset != 22 || service.listReq.Limit != 20 {
		t.Fatalf("List req = %#v, want compat filters and offset", service.listReq)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "ds-1" || result.Items[0].DocumentSizeBytes != 42 || result.Items[0].DocumentCount != 3 {
		t.Fatalf("List result = %#v, want mapped knowledge summary", result)
	}
	if result.Page.Total == nil || *result.Page.Total != 12 {
		t.Fatalf("total = %v, want 12", result.Page.Total)
	}
	nextOffset, err := contract.DecodeOffsetPageToken(result.Page.NextPageToken)
	if err != nil || nextOffset != 42 {
		t.Fatalf("next token offset=%d err=%v, want 42", nextOffset, err)
	}
}

func TestKnowledgeAdapterGetPassesDatasetIDAndMapsFields(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	service := &fakeDatasetCatalogService{
		getRes: doc.Dataset{
			DatasetID:     "ds-owned",
			DisplayName:   "Product Docs",
			Desc:          "API references",
			Tags:          []string{"api", "release"},
			UpdateTime:    now,
			DocumentSize:  12,
			DocumentCount: 1,
		},
	}
	adapter := mustKnowledgeAdapter(t, service)
	result, err := adapter.Get(context.Background(), contract.CallContext{UserID: " user-1 "}, compatknowledge.GetInput{KnowledgeID: " ds-owned "})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if service.getReq.UserID != "user-1" || service.getReq.DatasetID != "ds-owned" {
		t.Fatalf("Get req = %#v, want user/dataset", service.getReq)
	}
	got := result.Knowledge
	if got.ID != "ds-owned" || got.Name != "Product Docs" || got.Description != "API references" {
		t.Fatalf("summary = %#v, want dataset metadata", got)
	}
	if got.DocumentCount != 1 || got.DocumentSizeBytes != 12 || !got.UpdatedAt.Equal(now) {
		t.Fatalf("stats/time = %#v, want mapped values", got)
	}
}

func TestKnowledgeAdapterMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want contract.ErrorCode
	}{
		{name: "invalid", err: &doc.DatasetServiceError{Code: doc.DatasetServiceInvalidArgument, Message: "bad"}, want: contract.InvalidArgument},
		{name: "not found", err: &doc.DatasetServiceError{Code: doc.DatasetServiceNotFound, Message: "missing"}, want: contract.NotFound},
		{name: "forbidden", err: &doc.DatasetServiceError{Code: doc.DatasetServiceForbidden, Message: "forbidden"}, want: contract.NotFound},
		{name: "unavailable", err: &doc.DatasetServiceError{Code: doc.DatasetServiceUnavailable, Message: "db"}, want: contract.BackendUnavailable},
		{name: "gorm not found", err: gorm.ErrRecordNotFound, want: contract.NotFound},
		{name: "timeout", err: errors.New("connection refused"), want: contract.BackendUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := mustKnowledgeAdapter(t, &fakeDatasetCatalogService{getErr: tt.err})
			_, err := adapter.Get(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.GetInput{KnowledgeID: "ds-1"})
			if code, ok := contract.CodeOf(err); !ok || code != tt.want {
				t.Fatalf("code = %v, %v; want %s", code, ok, tt.want)
			}
		})
	}
}

func TestKnowledgeAdapterInvalidPageToken(t *testing.T) {
	adapter := mustKnowledgeAdapter(t, &fakeDatasetCatalogService{})
	_, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.ListInput{
		Page: contract.PageRequest{PageSize: 20, PageToken: "not-valid"},
	})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestNewKnowledgeCatalogAdapterRejectsNilDependencies(t *testing.T) {
	if _, err := NewKnowledgeCatalogAdapter(nil); err == nil {
		t.Fatalf("NewKnowledgeCatalogAdapter nil service error = nil, want error")
	}
	if _, err := NewKnowledgeCatalogAdapterForDB(nil); err == nil {
		t.Fatalf("NewKnowledgeCatalogAdapterForDB nil db error = nil, want error")
	}
}

func mustKnowledgeAdapter(t *testing.T, service DatasetCatalogService) *KnowledgeCatalogAdapter {
	t.Helper()
	adapter, err := NewKnowledgeCatalogAdapter(service)
	if err != nil {
		t.Fatalf("NewKnowledgeCatalogAdapter: %v", err)
	}
	return adapter
}
