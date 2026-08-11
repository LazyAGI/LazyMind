package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
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

type pagedDatasetCatalogService struct {
	items []doc.Dataset
	reqs  []doc.DatasetListRequest
}

func (s *pagedDatasetCatalogService) ListDatasets(ctx context.Context, req doc.DatasetListRequest) (doc.DatasetListResult, error) {
	s.reqs = append(s.reqs, req)
	start := req.Offset
	if start > len(s.items) {
		start = len(s.items)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = len(s.items)
	}
	end := start + limit
	if end > len(s.items) {
		end = len(s.items)
	}
	return doc.DatasetListResult{
		Datasets:   s.items[start:end],
		TotalSize:  int64(len(s.items)),
		NextOffset: end,
		HasMore:    end < len(s.items),
	}, nil
}

func (s *pagedDatasetCatalogService) GetDataset(ctx context.Context, req doc.DatasetGetRequest) (doc.Dataset, error) {
	return doc.Dataset{}, nil
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
	result, err := adapter.List(context.Background(), contract.CallContext{UserID: " user-1 ", TenantID: " tenant-a "}, compatknowledge.ListInput{
		Keyword: " docs ",
		Tags:    []string{"api"},
		Page:    contract.PageRequest{PageSize: 20, PageToken: contract.EncodeOffsetPageToken(22)},
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if service.listReq.UserID != "user-1" || service.listReq.Caller.UserID != "user-1" || service.listReq.Caller.TenantID != "tenant-a" {
		t.Fatalf("List user = %q caller=%q tenant=%q, want user-1/tenant-a", service.listReq.UserID, service.listReq.Caller.UserID, service.listReq.Caller.TenantID)
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

func TestKnowledgeAdapterPaginationUsesNextTokenWithoutDuplicates(t *testing.T) {
	service := &pagedDatasetCatalogService{
		items: []doc.Dataset{
			{DatasetID: "ds-1", DisplayName: "One"},
			{DatasetID: "ds-2", DisplayName: "Two"},
			{DatasetID: "ds-3", DisplayName: "Three"},
		},
	}
	adapter := mustKnowledgeAdapter(t, service)
	first, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.ListInput{
		Page: contract.PageRequest{PageSize: 2},
	})
	if err != nil {
		t.Fatalf("first List returned error: %v", err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != "ds-1" || first.Items[1].ID != "ds-2" || first.Page.NextPageToken == "" {
		t.Fatalf("first page = %#v, want ds-1/ds-2 with next token", first)
	}
	second, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.ListInput{
		Page: contract.PageRequest{PageSize: 2, PageToken: first.Page.NextPageToken},
	})
	if err != nil {
		t.Fatalf("second List returned error: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "ds-3" {
		t.Fatalf("second page = %#v, want ds-3", second)
	}
	if second.Page.NextPageToken != "" {
		t.Fatalf("second NextPageToken = %q, want empty", second.Page.NextPageToken)
	}
	if len(service.reqs) != 2 || service.reqs[1].Offset != 2 {
		t.Fatalf("service reqs = %#v, want second offset 2", service.reqs)
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
	result, err := adapter.Get(context.Background(), contract.CallContext{UserID: " user-1 ", TenantID: " tenant-a "}, compatknowledge.GetInput{KnowledgeID: " ds-owned "})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if service.getReq.UserID != "user-1" || service.getReq.DatasetID != "ds-owned" || service.getReq.Caller.TenantID != "tenant-a" {
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

func TestKnowledgeAdapterForwardsTenantIDToScan(t *testing.T) {
	db := newKnowledgeAdapterTestDB(t)
	var gotTenantID string
	installKnowledgeAdapterScanTransportWithAssertion(t, func(r *http.Request) {
		gotTenantID = r.Header.Get("X-Tenant-ID")
	})
	seedKnowledgeAdapterDataset(t, db, "ds-tenant", "user-1", "Tenant Dataset", time.Now().UTC())
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatalf("NewDatasetCatalogService: %v", err)
	}
	adapter := mustKnowledgeAdapter(t, service)
	if _, err := adapter.Get(context.Background(), contract.CallContext{UserID: "user-1", TenantID: "tenant-a"}, compatknowledge.GetInput{KnowledgeID: "ds-tenant"}); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if gotTenantID != "tenant-a" {
		t.Fatalf("scan X-Tenant-ID = %q, want tenant-a", gotTenantID)
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

func TestKnowledgeAdapterListUsesRealServiceUserIsolation(t *testing.T) {
	db := newKnowledgeAdapterTestDB(t)
	installKnowledgeAdapterScanTransport(t)
	seedKnowledgeAdapterDataset(t, db, "ds-user-a", "user-a", "Dataset A", time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC))
	seedKnowledgeAdapterDataset(t, db, "ds-user-b", "user-b", "Dataset B", time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC))
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatalf("NewDatasetCatalogService: %v", err)
	}
	adapter := mustKnowledgeAdapter(t, service)

	result, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-a"}, compatknowledge.ListInput{
		Page: contract.PageRequest{PageSize: 20},
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	ids := map[string]bool{}
	for _, item := range result.Items {
		ids[item.ID] = true
	}
	if !ids["ds-user-a"] {
		t.Fatalf("List items = %#v, want ds-user-a", result.Items)
	}
	if ids["ds-user-b"] {
		t.Fatalf("List items = %#v, must not include ds-user-b", result.Items)
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

type knowledgeAdapterRoundTripFunc func(*http.Request) (*http.Response, error)

func (f knowledgeAdapterRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newKnowledgeAdapterTestDB(t *testing.T) *orm.DB {
	t.Helper()
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "main")
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}, &orm.DefaultDataset{}, &orm.ACLModel{}, &orm.UserGroupModel{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	acl.InitStore(db)
	return db
}

func installKnowledgeAdapterScanTransport(t *testing.T) {
	installKnowledgeAdapterScanTransportWithAssertion(t, nil)
}

func installKnowledgeAdapterScanTransportWithAssertion(t *testing.T, assertRequest func(*http.Request)) {
	t.Helper()
	prevTransport := http.DefaultTransport
	http.DefaultTransport = knowledgeAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/scan/internal/source-access/by-dataset:batch":
			if assertRequest != nil {
				assertRequest(r)
			}
			return knowledgeAdapterJSONResponse(http.StatusOK, `{"items":[]}`), nil
		case "/api/scan/internal/sources/by-datasets":
			if assertRequest != nil {
				assertRequest(r)
			}
			return knowledgeAdapterJSONResponse(http.StatusOK, `{"source_map":{}}`), nil
		default:
			return knowledgeAdapterJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
}

func knowledgeAdapterJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func seedKnowledgeAdapterDataset(t *testing.T, db *orm.DB, id, userID, name string, updatedAt time.Time) {
	t.Helper()
	ext, err := json.Marshal(map[string]any{"tags": []string{}})
	if err != nil {
		t.Fatalf("marshal ext: %v", err)
	}
	if err := db.Create(&orm.Dataset{
		ID:           id,
		KbID:         "kb-" + id,
		DisplayName:  name,
		Desc:         "",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          ext,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      updatedAt,
			UpdatedAt:      updatedAt,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset %s: %v", id, err)
	}
}
