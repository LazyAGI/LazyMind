package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/compat/contract"
	compatknowledge "lazymind/core/compat/knowledge"
	"lazymind/core/doc"
	"lazymind/core/log"
)

type fakeDatasetSearchResolver struct {
	userID   string
	tenantID string
	ids      []string
	scope    DatasetSearchScope
	err      error
	calls    int
}

func (r *fakeDatasetSearchResolver) ResolveSearchDatasets(ctx context.Context, userID, tenantID string, datasetIDs []string) (DatasetSearchScope, error) {
	r.calls++
	r.userID = userID
	r.tenantID = tenantID
	r.ids = append([]string(nil), datasetIDs...)
	if r.err != nil {
		return DatasetSearchScope{}, r.err
	}
	return r.scope, nil
}

type fakePureSearchClient struct {
	req   PureKnowledgeSearchRequest
	resp  PureKnowledgeSearchResponse
	err   error
	calls int
}

func (c *fakePureSearchClient) Search(ctx context.Context, req PureKnowledgeSearchRequest) (PureKnowledgeSearchResponse, error) {
	c.calls++
	c.req = req
	if c.err != nil {
		return PureKnowledgeSearchResponse{}, c.err
	}
	return c.resp, nil
}

type fakeDocumentIDMapper struct {
	datasetIDs []string
	lazyDocIDs []string
	mapping    map[documentMapKey]string
	err        error
	calls      int
}

type fakeDatasetGetter struct {
	errByID map[string]error
	calls   []doc.DatasetGetRequest
}

func (g *fakeDatasetGetter) GetDataset(ctx context.Context, req doc.DatasetGetRequest) (doc.Dataset, error) {
	g.calls = append(g.calls, req)
	if err := g.errByID[req.DatasetID]; err != nil {
		return doc.Dataset{}, err
	}
	return doc.Dataset{DatasetID: req.DatasetID}, nil
}

func (m *fakeDocumentIDMapper) MapCoreDocumentIDs(ctx context.Context, datasetIDs []string, lazyDocIDs []string) (map[documentMapKey]string, error) {
	m.calls++
	m.datasetIDs = append([]string(nil), datasetIDs...)
	m.lazyDocIDs = append([]string(nil), lazyDocIDs...)
	if m.err != nil {
		return nil, m.err
	}
	return m.mapping, nil
}

func TestKnowledgeSearchAdapterUsesKBIDsAndMapsHits(t *testing.T) {
	resolver := &fakeDatasetSearchResolver{scope: DatasetSearchScope{
		DatasetIDToKBID: map[string]string{"ds_core_001": "kb_backend_901", "ds_core_002": "kb_backend_902"},
		KBIDToDatasetID: map[string]string{"kb_backend_901": "ds_core_001", "kb_backend_902": "ds_core_002"},
	}}
	client := &fakePureSearchClient{resp: PureKnowledgeSearchResponse{Hits: []PureKnowledgeSearchHit{
		{KBID: "kb_backend_901", DocID: "lazy_doc_1", ChunkID: "chunk-1", Text: "alpha", Score: 0.7, Title: "A", SourceURL: "https://files.test/a"},
		{KBID: "kb_backend_902", DocID: "lazy_doc_2", ChunkID: "chunk-2", Text: "beta", Score: 0.2, Title: "B"},
	}}}
	mapper := &fakeDocumentIDMapper{mapping: map[documentMapKey]string{
		{DatasetID: "ds_core_001", LazyDocID: "lazy_doc_1"}: "doc_core_1",
		{DatasetID: "ds_core_002", LazyDocID: "lazy_doc_2"}: "doc_core_2",
	}}
	adapter := mustPureSearchAdapter(t, resolver, client, mapper)

	got, err := adapter.Search(context.Background(), contract.CallContext{UserID: " user-1 ", TenantID: " tenant-a "}, compatknowledge.SearchInput{
		Query:        " q ",
		KnowledgeIDs: []string{"ds_core_001", "ds_core_002"},
		TopK:         7,
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if resolver.userID != "user-1" || resolver.tenantID != "tenant-a" || len(resolver.ids) != 2 || resolver.ids[0] != "ds_core_001" {
		t.Fatalf("unexpected resolver call: user=%q ids=%#v", resolver.userID, resolver.ids)
	}
	if client.req.UserID != "user-1" || client.req.Query != "q" || client.req.TopK != 7 || strings.Join(client.req.KBIDs, ",") != "kb_backend_901,kb_backend_902" {
		t.Fatalf("unexpected client request: %#v", client.req)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("hits = %#v, want 2", got.Hits)
	}
	if got.Hits[0].KnowledgeID != "ds_core_001" || got.Hits[0].DocumentID != "doc_core_1" || got.Hits[0].Score != 0.7 || got.Hits[0].SourceURL != "https://files.test/a" {
		t.Fatalf("first hit = %#v", got.Hits[0])
	}
	if got.Hits[1].KnowledgeID != "ds_core_002" || got.Hits[1].DocumentID != "doc_core_2" {
		t.Fatalf("second hit = %#v", got.Hits[1])
	}
}

func TestKnowledgeSearchAdapterDropsUnsafeAndUnmappedHits(t *testing.T) {
	var logBuf bytes.Buffer
	prevLogger := log.Logger
	log.Logger = log.Logger.Output(&logBuf)
	t.Cleanup(func() { log.Logger = prevLogger })

	resolver := &fakeDatasetSearchResolver{scope: DatasetSearchScope{
		DatasetIDToKBID: map[string]string{"ds_core_001": "kb_backend_901"},
		KBIDToDatasetID: map[string]string{"kb_backend_901": "ds_core_001"},
	}}
	client := &fakePureSearchClient{resp: PureKnowledgeSearchResponse{Hits: []PureKnowledgeSearchHit{
		{KBID: "kb_backend_901", DocID: "lazy_doc_1", ChunkID: "chunk-1", Text: "alpha", Score: 1},
		{KBID: "kb_backend_901", DocID: "lazy_missing", ChunkID: "chunk-2", Text: "missing", Score: 2},
		{KBID: "kb_other", DocID: "lazy_doc_1", ChunkID: "chunk-3", Text: "escaped", Score: 3},
		{KBID: "kb_backend_901", DocID: "lazy_doc_1", ChunkID: "chunk-img", Text: "/tmp/a.png", HitType: "image", Score: 4},
		{KBID: "kb_backend_901", DocID: "lazy_doc_1", ChunkID: "chunk-1", Text: "alpha", Score: 1},
		{KBID: "kb_backend_901", DocID: "lazy_doc_1", ChunkID: "chunk-4", Text: "local", SourceURL: "file:///tmp/a.txt", Score: 5},
	}}}
	mapper := &fakeDocumentIDMapper{mapping: map[documentMapKey]string{
		{DatasetID: "ds_core_001", LazyDocID: "lazy_doc_1"}: "doc_core_1",
	}}
	adapter := mustPureSearchAdapter(t, resolver, client, mapper)

	got, err := adapter.Search(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds_core_001"}, TopK: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("hits = %#v, want mapped text hit and sanitized local-url hit", got.Hits)
	}
	if got.Hits[0].DocumentID != "doc_core_1" || got.Hits[0].Text != "alpha" {
		t.Fatalf("first hit = %#v", got.Hits[0])
	}
	if got.Hits[1].SourceURL != "" || got.Hits[1].Text != "local" {
		t.Fatalf("unsafe source URL not cleared: %#v", got.Hits[1])
	}
	if strings.Contains(logBuf.String(), "lazy_doc") {
		t.Fatalf("log leaked lazy document id: %s", logBuf.String())
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"lazy_doc", "local_path", "metadata", "global_metadata", "docid"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestKnowledgeSearchAdapterEmptyResultsReturnsEmptySlice(t *testing.T) {
	adapter := mustPureSearchAdapter(t,
		&fakeDatasetSearchResolver{scope: DatasetSearchScope{DatasetIDToKBID: map[string]string{"ds": "kb"}, KBIDToDatasetID: map[string]string{"kb": "ds"}}},
		&fakePureSearchClient{},
		&fakeDocumentIDMapper{},
	)
	got, err := adapter.Search(context.Background(), contract.CallContext{UserID: "user"}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds"}, TopK: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if got.Hits == nil || len(got.Hits) != 0 {
		t.Fatalf("hits = %#v, want empty non-nil slice", got.Hits)
	}
}

func TestKnowledgeSearchAdapterStopsBeforeClientWhenResolverFails(t *testing.T) {
	resolverErr := contract.NewError(contract.NotFound, "resolve", "missing", false, gorm.ErrRecordNotFound)
	client := &fakePureSearchClient{}
	adapter := mustPureSearchAdapter(t, &fakeDatasetSearchResolver{err: resolverErr}, client, &fakeDocumentIDMapper{})

	_, err := adapter.Search(context.Background(), contract.CallContext{UserID: "user"}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds"}, TopK: 10})
	if !errors.Is(err, resolverErr) {
		t.Fatalf("err = %v, want resolver err", err)
	}
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want 0", client.calls)
	}
}

func TestKnowledgeSearchAdapterMapsClientAndMapperErrors(t *testing.T) {
	scope := DatasetSearchScope{DatasetIDToKBID: map[string]string{"ds": "kb"}, KBIDToDatasetID: map[string]string{"kb": "ds"}}
	clientErr := contract.NewError(contract.BackendUnavailable, "client", "down", true, context.DeadlineExceeded)
	adapter := mustPureSearchAdapter(t, &fakeDatasetSearchResolver{scope: scope}, &fakePureSearchClient{err: clientErr}, &fakeDocumentIDMapper{})
	_, err := adapter.Search(context.Background(), contract.CallContext{UserID: "user"}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds"}, TopK: 10})
	if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("client err = %v code=%v ok=%v", err, code, ok)
	}

	mapperErr := contract.NewError(contract.BackendUnavailable, "docs", "down", true, context.Canceled)
	adapter = mustPureSearchAdapter(t, &fakeDatasetSearchResolver{scope: scope}, &fakePureSearchClient{resp: PureKnowledgeSearchResponse{Hits: []PureKnowledgeSearchHit{{KBID: "kb", DocID: "lazy", Text: "x"}}}}, &fakeDocumentIDMapper{err: mapperErr})
	_, err = adapter.Search(context.Background(), contract.CallContext{UserID: "user"}, compatknowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds"}, TopK: 10})
	if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable || !errors.Is(err, context.Canceled) {
		t.Fatalf("mapper err = %v code=%v ok=%v", err, code, ok)
	}
}

func TestDBBackedDatasetSearchResolverUsesDatasetIDAndKBID(t *testing.T) {
	db := newKnowledgeAdapterTestDB(t)
	installKnowledgeAdapterScanTransport(t)
	now := time.Now().UTC()
	seedSearchDataset(t, db, "ds_core_001", "kb_backend_901", "user-1", now)

	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatalf("NewDatasetCatalogService: %v", err)
	}
	resolver, err := NewDBBackedDatasetSearchResolver(db.DB, service)
	if err != nil {
		t.Fatalf("NewDBBackedDatasetSearchResolver: %v", err)
	}
	scope, err := resolver.ResolveSearchDatasets(context.Background(), "user-1", "tenant-a", []string{"ds_core_001"})
	if err != nil {
		t.Fatalf("ResolveSearchDatasets: %v", err)
	}
	if scope.DatasetIDToKBID["ds_core_001"] != "kb_backend_901" || scope.KBIDToDatasetID["kb_backend_901"] != "ds_core_001" {
		t.Fatalf("scope = %#v", scope)
	}
}

func TestDBBackedDatasetSearchResolverErrors(t *testing.T) {
	db := newKnowledgeAdapterTestDB(t)
	installKnowledgeAdapterScanTransport(t)
	now := time.Now().UTC()
	seedSearchDataset(t, db, "ds_core_001", "kb_backend_901", "user-2", now)
	seedSearchDataset(t, db, "ds_core_forbidden_empty_kb", "", "user-2", now)
	seedSearchDataset(t, db, "ds_core_empty_kb", "", "user-1", now)
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatalf("NewDatasetCatalogService: %v", err)
	}
	resolver, err := NewDBBackedDatasetSearchResolver(db.DB, service)
	if err != nil {
		t.Fatalf("NewDBBackedDatasetSearchResolver: %v", err)
	}

	tests := []struct {
		name string
		ids  []string
		want contract.ErrorCode
	}{
		{name: "missing", ids: []string{"missing"}, want: contract.NotFound},
		{name: "forbidden nonempty kb", ids: []string{"ds_core_001"}, want: contract.NotFound},
		{name: "forbidden empty kb", ids: []string{"ds_core_forbidden_empty_kb"}, want: contract.NotFound},
		{name: "authorized empty kb", ids: []string{"ds_core_empty_kb"}, want: contract.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolver.ResolveSearchDatasets(context.Background(), "user-1", "tenant-a", tt.ids)
			if code, ok := contract.CodeOf(err); !ok || code != tt.want {
				t.Fatalf("code = %v ok=%v want %s err=%v", code, ok, tt.want, err)
			}
		})
	}
}

func TestKnowledgeSearchAdapterForbiddenDatasetNeverCallsClient(t *testing.T) {
	db := newKnowledgeAdapterTestDB(t)
	installKnowledgeAdapterScanTransport(t)
	now := time.Now().UTC()
	seedSearchDataset(t, db, "ds_core_forbidden_kb", "kb_backend_forbidden", "user-2", now)
	seedSearchDataset(t, db, "ds_core_forbidden_empty_kb", "", "user-2", now)
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatalf("NewDatasetCatalogService: %v", err)
	}
	resolver, err := NewDBBackedDatasetSearchResolver(db.DB, service)
	if err != nil {
		t.Fatalf("NewDBBackedDatasetSearchResolver: %v", err)
	}
	for _, datasetID := range []string{"ds_core_forbidden_kb", "ds_core_forbidden_empty_kb"} {
		t.Run(datasetID, func(t *testing.T) {
			client := &fakePureSearchClient{}
			adapter := mustPureSearchAdapter(t, resolver, client, &fakeDocumentIDMapper{})
			_, err := adapter.Search(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.SearchInput{
				Query:        "q",
				KnowledgeIDs: []string{datasetID},
				TopK:         10,
			})
			if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
				t.Fatalf("code = %v ok=%v want NOT_FOUND err=%v", code, ok, err)
			}
			if client.calls != 0 {
				t.Fatalf("client calls = %d, want 0", client.calls)
			}
		})
	}
}

func TestDBBackedDatasetSearchResolverDBErrorAfterACL(t *testing.T) {
	db := newKnowledgeAdapterTestDB(t)
	seedSearchDataset(t, db, "ds_core_001", "kb_backend_901", "user-1", time.Now().UTC())
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("sql DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close DB: %v", err)
	}
	getter := &fakeDatasetGetter{}
	resolver, err := NewDBBackedDatasetSearchResolver(db.DB, getter)
	if err != nil {
		t.Fatalf("NewDBBackedDatasetSearchResolver: %v", err)
	}
	_, err = resolver.ResolveSearchDatasets(context.Background(), "user-1", "tenant-a", []string{"ds_core_001"})
	if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable {
		t.Fatalf("code = %v ok=%v want BACKEND_UNAVAILABLE err=%v", code, ok, err)
	}
	if len(getter.calls) != 1 || getter.calls[0].DatasetID != "ds_core_001" {
		t.Fatalf("ACL calls = %#v, want one call before DB read", getter.calls)
	}
}

func TestDBBackedDocumentIDMapperMapsOnlyRequestedDatasets(t *testing.T) {
	db := newKnowledgeAdapterTestDB(t)
	now := time.Now().UTC()
	seedSearchDataset(t, db, "ds_core_001", "kb_backend_901", "user-1", now)
	seedSearchDataset(t, db, "ds_core_002", "kb_backend_902", "user-1", now)
	seedSearchDocument(t, db, "doc_core_1", "ds_core_001", "lazy_doc_1", now)
	seedSearchDocument(t, db, "doc_core_2", "ds_core_002", "lazy_doc_1", now)
	seedSearchDocument(t, db, "doc_deleted", "ds_core_001", "lazy_deleted", now)
	if err := db.Model(&orm.Document{}).Where("id = ?", "doc_deleted").Update("deleted_at", now).Error; err != nil {
		t.Fatalf("soft delete document: %v", err)
	}

	mapper, err := NewDBBackedDocumentIDMapper(db.DB)
	if err != nil {
		t.Fatalf("NewDBBackedDocumentIDMapper: %v", err)
	}
	got, err := mapper.MapCoreDocumentIDs(context.Background(), []string{"ds_core_001"}, []string{"lazy_doc_1", "lazy_deleted"})
	if err != nil {
		t.Fatalf("MapCoreDocumentIDs: %v", err)
	}
	if got[documentMapKey{DatasetID: "ds_core_001", LazyDocID: "lazy_doc_1"}] != "doc_core_1" {
		t.Fatalf("mapping = %#v", got)
	}
	if _, ok := got[documentMapKey{DatasetID: "ds_core_002", LazyDocID: "lazy_doc_1"}]; ok {
		t.Fatalf("mapping crossed dataset: %#v", got)
	}
	if _, ok := got[documentMapKey{DatasetID: "ds_core_001", LazyDocID: "lazy_deleted"}]; ok {
		t.Fatalf("mapping included soft-deleted document: %#v", got)
	}
}

func TestHTTPPureKnowledgeSearchClient(t *testing.T) {
	t.Setenv(internalServiceTokenEnv, "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/knowledge:search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get(internalServiceTokenHeader); got != "secret-token" {
			t.Fatalf("internal token header = %q", got)
		}
		var req struct {
			UserID string   `json:"user_id"`
			Query  string   `json:"query"`
			KBIDs  []string `json:"kb_ids"`
			TopK   int      `json:"top_k"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.UserID != "user-1" || req.Query != "q" || req.KBIDs[0] != "kb_backend_901" || req.TopK != 10 {
			t.Fatalf("request = %#v", req)
		}
		_, _ = w.Write([]byte(`{"hits":[{"kb_id":"kb_backend_901","doc_id":"lazy_doc_1","chunk_id":"chunk","text":"text","score":0.4,"title":"doc"}]}`))
	}))
	t.Cleanup(server.Close)
	client, err := NewHTTPPureKnowledgeSearchClient(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPPureKnowledgeSearchClient: %v", err)
	}
	got, err := client.Search(context.Background(), PureKnowledgeSearchRequest{UserID: "user-1", Query: "q", KBIDs: []string{"kb_backend_901"}, TopK: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Hits) != 1 || got.Hits[0].DocID != "lazy_doc_1" {
		t.Fatalf("hits = %#v", got.Hits)
	}
}

func TestHTTPPureKnowledgeSearchClientMapsErrors(t *testing.T) {
	t.Setenv(internalServiceTokenEnv, "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	client, err := NewHTTPPureKnowledgeSearchClient(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPPureKnowledgeSearchClient: %v", err)
	}
	_, err = client.Search(context.Background(), PureKnowledgeSearchRequest{Query: "q", KBIDs: []string{"kb"}, TopK: 10})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("code = %v ok=%v want INVALID_ARGUMENT", code, ok)
	}
}

func TestHTTPPureKnowledgeSearchClientRequiresInternalToken(t *testing.T) {
	t.Setenv(internalServiceTokenEnv, "")
	if _, err := NewHTTPPureKnowledgeSearchClient("http://search.test"); err == nil {
		t.Fatalf("NewHTTPPureKnowledgeSearchClient missing token error = nil, want error")
	}
}

func TestHTTPPureKnowledgeSearchClientMapsUnauthorizedAndLargeResponses(t *testing.T) {
	t.Setenv(internalServiceTokenEnv, "secret-token")
	t.Run("empty hits", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"hits":[]}`))
		}))
		t.Cleanup(server.Close)
		client, err := NewHTTPPureKnowledgeSearchClient(server.URL)
		if err != nil {
			t.Fatalf("NewHTTPPureKnowledgeSearchClient: %v", err)
		}
		got, err := client.Search(context.Background(), PureKnowledgeSearchRequest{Query: "q", KBIDs: []string{"kb"}, TopK: 10})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if got.Hits == nil || len(got.Hits) != 0 {
			t.Fatalf("hits = %#v, want empty non-nil slice", got.Hits)
		}
	})
	t.Run("unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		t.Cleanup(server.Close)
		client, err := NewHTTPPureKnowledgeSearchClient(server.URL)
		if err != nil {
			t.Fatalf("NewHTTPPureKnowledgeSearchClient: %v", err)
		}
		_, err = client.Search(context.Background(), PureKnowledgeSearchRequest{Query: "q", KBIDs: []string{"kb"}, TopK: 10})
		if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable {
			t.Fatalf("code = %v ok=%v want BACKEND_UNAVAILABLE err=%v", code, ok, err)
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"hits":[`))
		}))
		t.Cleanup(server.Close)
		client, err := NewHTTPPureKnowledgeSearchClient(server.URL)
		if err != nil {
			t.Fatalf("NewHTTPPureKnowledgeSearchClient: %v", err)
		}
		_, err = client.Search(context.Background(), PureKnowledgeSearchRequest{Query: "q", KBIDs: []string{"kb"}, TopK: 10})
		if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable {
			t.Fatalf("code = %v ok=%v want BACKEND_UNAVAILABLE err=%v", code, ok, err)
		}
	})
	t.Run("large response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(bytes.Repeat([]byte("x"), pureKnowledgeSearchMaxResponseBytes+1))
		}))
		t.Cleanup(server.Close)
		client, err := NewHTTPPureKnowledgeSearchClient(server.URL)
		if err != nil {
			t.Fatalf("NewHTTPPureKnowledgeSearchClient: %v", err)
		}
		_, err = client.Search(context.Background(), PureKnowledgeSearchRequest{Query: "q", KBIDs: []string{"kb"}, TopK: 10})
		if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable {
			t.Fatalf("code = %v ok=%v want BACKEND_UNAVAILABLE err=%v", code, ok, err)
		}
	})
}

func TestHTTPPureKnowledgeSearchClientPreservesContextCause(t *testing.T) {
	t.Setenv(internalServiceTokenEnv, "secret-token")
	client, err := NewHTTPPureKnowledgeSearchClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewHTTPPureKnowledgeSearchClient: %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Search(canceled, PureKnowledgeSearchRequest{Query: "q", KBIDs: []string{"kb"}, TopK: 10})
	if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err = %v code=%v ok=%v", err, code, ok)
	}

	deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = client.Search(deadline, PureKnowledgeSearchRequest{Query: "q", KBIDs: []string{"kb"}, TopK: 10})
	if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline err = %v code=%v ok=%v", err, code, ok)
	}
}

func TestKnowledgeSearchAdapterForDBRequiresDependencies(t *testing.T) {
	if _, err := NewKnowledgeSearchAdapterForDB(nil, "http://search"); err == nil {
		t.Fatalf("NewKnowledgeSearchAdapterForDB nil db error = nil, want error")
	}
	if _, err := NewKnowledgeSearchAdapterForDB(&gorm.DB{}, " "); err == nil {
		t.Fatalf("NewKnowledgeSearchAdapterForDB empty endpoint error = nil, want error")
	}
}

func mustPureSearchAdapter(t *testing.T, resolver DatasetSearchResolver, client PureKnowledgeSearchClient, mapper DocumentIDMapper) *KnowledgeSearchAdapter {
	t.Helper()
	adapter, err := NewKnowledgeSearchAdapter(resolver, client, mapper)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchAdapter: %v", err)
	}
	return adapter
}

func seedSearchDataset(t *testing.T, db *orm.DB, datasetID, kbID, ownerID string, now time.Time) {
	t.Helper()
	if err := db.Create(&orm.Dataset{
		ID:          datasetID,
		KbID:        kbID,
		DisplayName: "Dataset " + datasetID,
		Desc:        "desc",
		BaseModel: orm.BaseModel{
			CreateUserID:   ownerID,
			CreateUserName: ownerID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("seed dataset: %v", err)
	}
}

func seedSearchDocument(t *testing.T, db *orm.DB, docID, datasetID, lazyDocID string, now time.Time) {
	t.Helper()
	if err := db.Create(&orm.Document{
		ID:           docID,
		DatasetID:    datasetID,
		LazyllmDocID: lazyDocID,
		DisplayName:  docID + ".txt",
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "user-1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("seed document: %v", err)
	}
}
