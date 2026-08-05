package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/compat/contract"
	compatknowledge "lazymind/core/compat/knowledge"
	"lazymind/core/doc"
	"lazymind/core/log"
)

const (
	pureKnowledgeSearchPath             = "/internal/knowledge:search"
	pureKnowledgeSearchMaxResponseBytes = 4 << 20
	internalServiceTokenEnv             = "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"
	internalServiceTokenHeader          = "X-LazyMind-Internal-Token"
)

type DatasetGetter interface {
	GetDataset(ctx context.Context, req doc.DatasetGetRequest) (doc.Dataset, error)
}

type DatasetSearchResolver interface {
	ResolveSearchDatasets(ctx context.Context, userID string, datasetIDs []string) (DatasetSearchScope, error)
}

type DatasetSearchScope struct {
	DatasetIDToKBID map[string]string
	KBIDToDatasetID map[string]string
}

type DBBackedDatasetSearchResolver struct {
	db       *gorm.DB
	datasets DatasetGetter
}

func NewDBBackedDatasetSearchResolver(db *gorm.DB, datasets DatasetGetter) (*DBBackedDatasetSearchResolver, error) {
	if db == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.resolver.new", "gorm db is required", false, nil)
	}
	if datasets == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.resolver.new", "dataset service is required", false, nil)
	}
	return &DBBackedDatasetSearchResolver{db: db, datasets: datasets}, nil
}

func (r *DBBackedDatasetSearchResolver) ResolveSearchDatasets(ctx context.Context, userID string, datasetIDs []string) (DatasetSearchScope, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || len(datasetIDs) == 0 {
		return DatasetSearchScope{}, contract.InvalidArgumentError("knowledge.search.resolve", "user_id and knowledge_ids are required")
	}

	for _, datasetID := range datasetIDs {
		if _, err := r.datasets.GetDataset(ctx, doc.DatasetGetRequest{
			UserID:    userID,
			DatasetID: datasetID,
			Caller:    doc.DatasetCatalogCaller{UserID: userID},
		}); err != nil {
			return DatasetSearchScope{}, mapDatasetSearchError("knowledge.search.resolve", err)
		}
	}

	var rows []orm.Dataset
	if err := r.db.WithContext(ctx).
		Where("id IN ? AND deleted_at IS NULL", datasetIDs).
		Find(&rows).Error; err != nil {
		return DatasetSearchScope{}, contract.NewError(contract.BackendUnavailable, "knowledge.search.resolve", "query datasets failed", true, err)
	}
	rowByID := make(map[string]orm.Dataset, len(rows))
	for _, row := range rows {
		rowByID[row.ID] = row
	}

	scope := DatasetSearchScope{
		DatasetIDToKBID: make(map[string]string, len(datasetIDs)),
		KBIDToDatasetID: make(map[string]string, len(datasetIDs)),
	}
	for _, datasetID := range datasetIDs {
		row, ok := rowByID[datasetID]
		if !ok {
			return DatasetSearchScope{}, contract.NewError(contract.NotFound, "knowledge.search.resolve", "knowledge not found", false, gorm.ErrRecordNotFound)
		}
		kbID := strings.TrimSpace(row.KbID)
		if kbID == "" {
			return DatasetSearchScope{}, contract.NewError(contract.Internal, "knowledge.search.resolve", "knowledge backend id is empty", false, nil)
		}
		scope.DatasetIDToKBID[datasetID] = kbID
		scope.KBIDToDatasetID[kbID] = datasetID
	}
	return scope, nil
}

type PureKnowledgeSearchClient interface {
	Search(ctx context.Context, req PureKnowledgeSearchRequest) (PureKnowledgeSearchResponse, error)
}

type PureKnowledgeSearchRequest struct {
	Query string
	KBIDs []string
	TopK  int
}

type PureKnowledgeSearchResponse struct {
	Hits []PureKnowledgeSearchHit
}

type PureKnowledgeSearchHit struct {
	KBID      string
	DocID     string
	ChunkID   string
	Text      string
	Score     float64
	SourceURL string
	Title     string
	HitType   string
	Group     string
}

type HTTPPureKnowledgeSearchClient struct {
	baseURL string
	token   string
	timeout time.Duration
}

func NewHTTPPureKnowledgeSearchClient(baseURL string) (*HTTPPureKnowledgeSearchClient, error) {
	return NewHTTPPureKnowledgeSearchClientWithToken(baseURL, os.Getenv(internalServiceTokenEnv))
}

func NewHTTPPureKnowledgeSearchClientWithToken(baseURL, token string) (*HTTPPureKnowledgeSearchClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, contract.NewError(contract.Unsupported, "knowledge.search.client.new", "knowledge search endpoint is required", false, nil)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, contract.NewError(contract.Unsupported, "knowledge.search.client.new", "internal service token is required", false, nil)
	}
	return &HTTPPureKnowledgeSearchClient{baseURL: baseURL, token: token, timeout: 60 * time.Second}, nil
}

func (c *HTTPPureKnowledgeSearchClient) Search(ctx context.Context, req PureKnowledgeSearchRequest) (PureKnowledgeSearchResponse, error) {
	var resp struct {
		Hits []struct {
			KBID      string  `json:"kb_id"`
			DocID     string  `json:"doc_id"`
			ChunkID   string  `json:"chunk_id"`
			Text      string  `json:"text"`
			Score     float64 `json:"score"`
			SourceURL string  `json:"source_url"`
			Title     string  `json:"title"`
			HitType   string  `json:"hit_type"`
			Group     string  `json:"group"`
		} `json:"hits"`
	}
	payload := map[string]any{
		"query":  req.Query,
		"kb_ids": req.KBIDs,
		"top_k":  req.TopK,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return PureKnowledgeSearchResponse{}, contract.NewError(contract.Internal, "knowledge.search.client", "encode search request failed", false, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(c.baseURL, pureKnowledgeSearchPath), bytes.NewReader(body))
	if err != nil {
		return PureKnowledgeSearchResponse{}, contract.NewError(contract.Internal, "knowledge.search.client", "build search request failed", false, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(internalServiceTokenHeader, c.token)

	httpClient := http.Client{Timeout: c.timeout}
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return PureKnowledgeSearchResponse{}, mapPureSearchClientError(err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, pureKnowledgeSearchMaxResponseBytes+1))
	if err != nil {
		return PureKnowledgeSearchResponse{}, contract.NewError(contract.BackendUnavailable, "knowledge.search.client", "read search response failed", true, err)
	}
	if int64(len(respBytes)) > pureKnowledgeSearchMaxResponseBytes {
		return PureKnowledgeSearchResponse{}, contract.NewError(contract.BackendUnavailable, "knowledge.search.client", "search response too large", true, nil)
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return PureKnowledgeSearchResponse{}, mapPureSearchStatus(httpResp.StatusCode)
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return PureKnowledgeSearchResponse{}, contract.NewError(contract.BackendUnavailable, "knowledge.search.client", "decode search response failed", true, err)
	}
	out := PureKnowledgeSearchResponse{Hits: make([]PureKnowledgeSearchHit, 0, len(resp.Hits))}
	for _, hit := range resp.Hits {
		out.Hits = append(out.Hits, PureKnowledgeSearchHit{
			KBID:      hit.KBID,
			DocID:     hit.DocID,
			ChunkID:   hit.ChunkID,
			Text:      hit.Text,
			Score:     hit.Score,
			SourceURL: hit.SourceURL,
			Title:     hit.Title,
			HitType:   hit.HitType,
			Group:     hit.Group,
		})
	}
	return out, nil
}

type DocumentIDMapper interface {
	MapCoreDocumentIDs(ctx context.Context, datasetIDs []string, lazyDocIDs []string) (map[documentMapKey]string, error)
}

type documentMapKey struct {
	DatasetID string
	LazyDocID string
}

type DBBackedDocumentIDMapper struct {
	db *gorm.DB
}

func NewDBBackedDocumentIDMapper(db *gorm.DB) (*DBBackedDocumentIDMapper, error) {
	if db == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.documents.new", "gorm db is required", false, nil)
	}
	return &DBBackedDocumentIDMapper{db: db}, nil
}

func (m *DBBackedDocumentIDMapper) MapCoreDocumentIDs(ctx context.Context, datasetIDs []string, lazyDocIDs []string) (map[documentMapKey]string, error) {
	if len(datasetIDs) == 0 || len(lazyDocIDs) == 0 {
		return map[documentMapKey]string{}, nil
	}
	var rows []orm.Document
	if err := m.db.WithContext(ctx).
		Select("id, dataset_id, lazyllm_doc_id").
		Where("dataset_id IN ? AND lazyllm_doc_id IN ? AND deleted_at IS NULL", datasetIDs, lazyDocIDs).
		Find(&rows).Error; err != nil {
		return nil, contract.NewError(contract.BackendUnavailable, "knowledge.search.documents", "query documents failed", true, err)
	}
	out := make(map[documentMapKey]string, len(rows))
	for _, row := range rows {
		out[documentMapKey{DatasetID: strings.TrimSpace(row.DatasetID), LazyDocID: strings.TrimSpace(row.LazyllmDocID)}] = strings.TrimSpace(row.ID)
	}
	return out, nil
}

type KnowledgeSearchAdapter struct {
	datasets  DatasetSearchResolver
	client    PureKnowledgeSearchClient
	documents DocumentIDMapper
}

func NewKnowledgeSearchAdapter(datasets DatasetSearchResolver, client PureKnowledgeSearchClient, documents DocumentIDMapper) (*KnowledgeSearchAdapter, error) {
	if datasets == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.adapter.new", "dataset resolver is required", false, nil)
	}
	if client == nil {
		return nil, contract.NewError(contract.Unsupported, "knowledge.search.adapter.new", "knowledge search client is required", false, nil)
	}
	if documents == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.adapter.new", "document mapper is required", false, nil)
	}
	return &KnowledgeSearchAdapter{datasets: datasets, client: client, documents: documents}, nil
}

func NewKnowledgeSearchAdapterForDB(db *gorm.DB, searchBaseURL string) (*KnowledgeSearchAdapter, error) {
	if db == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.search.adapter.new", "gorm db is required", false, nil)
	}
	datasets, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		return nil, mapDatasetServiceError("knowledge.search.adapter.new", err)
	}
	resolver, err := NewDBBackedDatasetSearchResolver(db, datasets)
	if err != nil {
		return nil, err
	}
	client, err := NewHTTPPureKnowledgeSearchClient(searchBaseURL)
	if err != nil {
		return nil, err
	}
	documents, err := NewDBBackedDocumentIDMapper(db)
	if err != nil {
		return nil, err
	}
	return NewKnowledgeSearchAdapter(resolver, client, documents)
}

func (a *KnowledgeSearchAdapter) Search(ctx context.Context, callCtx contract.CallContext, input compatknowledge.SearchInput) (compatknowledge.SearchResult, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return compatknowledge.SearchResult{}, contract.InvalidArgumentError("knowledge.search", "user_id is required")
	}
	scope, err := a.datasets.ResolveSearchDatasets(ctx, userID, input.KnowledgeIDs)
	if err != nil {
		return compatknowledge.SearchResult{}, err
	}
	kbIDs := make([]string, 0, len(input.KnowledgeIDs))
	for _, datasetID := range input.KnowledgeIDs {
		kbIDs = append(kbIDs, scope.DatasetIDToKBID[datasetID])
	}
	resp, err := a.client.Search(ctx, PureKnowledgeSearchRequest{
		Query: strings.TrimSpace(input.Query),
		KBIDs: kbIDs,
		TopK:  input.TopK,
	})
	if err != nil {
		return compatknowledge.SearchResult{}, err
	}
	return a.mapHits(ctx, input.KnowledgeIDs, scope, resp.Hits)
}

func (a *KnowledgeSearchAdapter) mapHits(ctx context.Context, datasetIDs []string, scope DatasetSearchScope, hits []PureKnowledgeSearchHit) (compatknowledge.SearchResult, error) {
	lazyDocIDs := make([]string, 0, len(hits))
	seenLazy := map[string]struct{}{}
	for _, hit := range hits {
		if isImageOnlyHit(hit) {
			continue
		}
		if _, ok := scope.KBIDToDatasetID[strings.TrimSpace(hit.KBID)]; !ok {
			continue
		}
		lazyDocID := strings.TrimSpace(hit.DocID)
		if lazyDocID == "" {
			continue
		}
		if _, ok := seenLazy[lazyDocID]; ok {
			continue
		}
		seenLazy[lazyDocID] = struct{}{}
		lazyDocIDs = append(lazyDocIDs, lazyDocID)
	}
	docMap, err := a.documents.MapCoreDocumentIDs(ctx, datasetIDs, lazyDocIDs)
	if err != nil {
		return compatknowledge.SearchResult{}, err
	}

	out := make([]compatknowledge.SearchHit, 0, len(hits))
	seenHits := map[string]struct{}{}
	droppedUnmapped := 0
	for _, hit := range hits {
		if isImageOnlyHit(hit) {
			continue
		}
		kbID := strings.TrimSpace(hit.KBID)
		datasetID, ok := scope.KBIDToDatasetID[kbID]
		if !ok {
			continue
		}
		lazyDocID := strings.TrimSpace(hit.DocID)
		coreDocID := strings.TrimSpace(docMap[documentMapKey{DatasetID: datasetID, LazyDocID: lazyDocID}])
		if coreDocID == "" {
			droppedUnmapped++
			continue
		}
		text := strings.TrimSpace(hit.Text)
		if text == "" {
			continue
		}
		chunkID := strings.TrimSpace(hit.ChunkID)
		key := datasetID + "\x00" + coreDocID + "\x00" + chunkID + "\x00" + text
		if _, ok := seenHits[key]; ok {
			continue
		}
		seenHits[key] = struct{}{}
		out = append(out, compatknowledge.SearchHit{
			KnowledgeID: datasetID,
			DocumentID:  coreDocID,
			ChunkID:     chunkID,
			Text:        text,
			Score:       hit.Score,
			SourceURL:   sanitizeSearchSourceURL(hit.SourceURL),
			Title:       strings.TrimSpace(hit.Title),
		})
	}
	if droppedUnmapped > 0 {
		log.Logger.Warn().
			Int("dropped_hits", droppedUnmapped).
			Int("dataset_count", len(datasetIDs)).
			Msg("knowledge search hits dropped because document mappings were not found")
	}
	if out == nil {
		out = []compatknowledge.SearchHit{}
	}
	return compatknowledge.SearchResult{Hits: out}, nil
}

func isImageOnlyHit(hit PureKnowledgeSearchHit) bool {
	return strings.EqualFold(strings.TrimSpace(hit.HitType), "image") ||
		strings.EqualFold(strings.TrimSpace(hit.Group), "image")
}

func sanitizeSearchSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		if parsed.Host != "" {
			return raw
		}
	}
	return ""
}

func mapPureSearchStatus(statusCode int) error {
	if statusCode == http.StatusBadRequest {
		return contract.NewError(contract.InvalidArgument, "knowledge.search.client", "invalid search request", false, nil)
	}
	return contract.NewError(contract.BackendUnavailable, "knowledge.search.client", "backend unavailable", true, nil)
}

func mapDatasetSearchError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var compatErr *contract.Error
	if errors.As(err, &compatErr) {
		return err
	}
	var svcErr *doc.DatasetServiceError
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case doc.DatasetServiceInvalidArgument:
			return contract.NewError(contract.InvalidArgument, operation, svcErr.Message, false, err)
		case doc.DatasetServiceNotFound, doc.DatasetServiceForbidden:
			return contract.NewError(contract.NotFound, operation, "knowledge not found", false, err)
		case doc.DatasetServiceUnavailable:
			return contract.NewError(contract.BackendUnavailable, operation, "backend unavailable", true, err)
		default:
			return contract.NewError(contract.Internal, operation, "internal error", false, err)
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return contract.NewError(contract.NotFound, operation, "knowledge not found", false, err)
	}
	return contract.NewError(contract.Internal, operation, "internal error", false, err)
}

func mapPureSearchClientError(err error) error {
	if err == nil {
		return nil
	}
	var compatErr *contract.Error
	if errors.As(err, &compatErr) {
		return err
	}
	return contract.NewError(contract.BackendUnavailable, "knowledge.search.client", "backend unavailable", true, err)
}
