package coreadapter

import (
	"context"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/doc"
	"lazymind/core/log"
)

type KnowledgeSearcher struct {
	scope     KnowledgeScopeResolver
	client    KnowledgeSearchBackendClient
	documents DocumentIDMapper
	models    RetrievalModelConfigLoader
}

func NewKnowledgeSearcher(scope KnowledgeScopeResolver, client KnowledgeSearchBackendClient, documents DocumentIDMapper, models RetrievalModelConfigLoader) (*KnowledgeSearcher, error) {
	if scope == nil || client == nil || documents == nil || models == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.search.adapter.new", "scope resolver, search client, document mapper and model config loader are required", false, nil)
	}
	return &KnowledgeSearcher{scope: scope, client: client, documents: documents, models: models}, nil
}

func NewKnowledgeSearcherForDB(db *gorm.DB, searchBaseURL, internalToken string, httpClient *http.Client) (*KnowledgeSearcher, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.search.adapter.new", "gorm db is required", false, nil)
	}
	datasets, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		return nil, mapDatasetError("knowledge.search.adapter.new", err)
	}
	scope, err := NewDBBackedKnowledgeScopeResolver(db, datasets)
	if err != nil {
		return nil, err
	}
	client, err := NewHTTPKnowledgeSearchBackendClient(searchBaseURL, internalToken, httpClient)
	if err != nil {
		return nil, err
	}
	documents, err := NewDBBackedDocumentIDMapper(db)
	if err != nil {
		return nil, err
	}
	models, err := NewDBBackedRetrievalModelConfigLoader(db)
	if err != nil {
		return nil, err
	}
	return NewKnowledgeSearcher(scope, client, documents, models)
}

func (s *KnowledgeSearcher) SearchKnowledge(ctx context.Context, call capability.InvocationContext, input capability.SearchKnowledgeInput) (capability.SearchKnowledgeResult, error) {
	userID := strings.TrimSpace(call.Principal.UserID)
	scope, err := s.scope.ResolveKnowledge(ctx, userID, strings.TrimSpace(call.Principal.TenantID), input.KnowledgeIDs)
	if err != nil {
		return capability.SearchKnowledgeResult{}, err
	}
	kbIDs := make([]string, 0, len(input.KnowledgeIDs))
	for _, datasetID := range input.KnowledgeIDs {
		kbIDs = append(kbIDs, scope.DatasetIDToKBID[datasetID])
	}
	modelConfig, err := s.models.LoadRetrievalModelConfig(ctx, userID)
	if err != nil {
		return capability.SearchKnowledgeResult{}, err
	}
	response, err := s.client.Search(ctx, KnowledgeSearchBackendRequest{
		UserID: userID, Query: input.Query, KBIDs: kbIDs, TopK: input.TopK, LLMConfig: modelConfig,
	})
	if err != nil {
		return capability.SearchKnowledgeResult{}, err
	}
	return s.mapHits(ctx, input.KnowledgeIDs, scope, response.Hits)
}

func (s *KnowledgeSearcher) mapHits(ctx context.Context, datasetIDs []string, scope KnowledgeScope, hits []KnowledgeSearchBackendHit) (capability.SearchKnowledgeResult, error) {
	lazyDocumentIDs := make([]string, 0, len(hits))
	seenDocuments := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if _, ok := scope.KBIDToDatasetID[strings.TrimSpace(hit.KBID)]; !ok {
			continue
		}
		lazyDocumentID := strings.TrimSpace(hit.DocID)
		if lazyDocumentID == "" {
			continue
		}
		if _, ok := seenDocuments[lazyDocumentID]; ok {
			continue
		}
		seenDocuments[lazyDocumentID] = struct{}{}
		lazyDocumentIDs = append(lazyDocumentIDs, lazyDocumentID)
	}
	documentIDs, err := s.documents.MapCoreDocumentIDs(ctx, datasetIDs, lazyDocumentIDs)
	if err != nil {
		return capability.SearchKnowledgeResult{}, err
	}
	result := capability.SearchKnowledgeResult{Hits: make([]capability.KnowledgeSearchHit, 0, len(hits))}
	seenHits := make(map[string]struct{}, len(hits))
	droppedUnmapped := 0
	for _, hit := range hits {
		datasetID, ok := scope.KBIDToDatasetID[strings.TrimSpace(hit.KBID)]
		if !ok {
			continue
		}
		documentID := strings.TrimSpace(documentIDs[documentMapKey{DatasetID: datasetID, LazyDocID: strings.TrimSpace(hit.DocID)}])
		if documentID == "" {
			droppedUnmapped++
			continue
		}
		text := strings.TrimSpace(hit.Text)
		if text == "" {
			continue
		}
		chunkID := strings.TrimSpace(hit.ChunkID)
		key := datasetID + "\x00" + documentID + "\x00" + chunkID + "\x00" + text
		if _, ok := seenHits[key]; ok {
			continue
		}
		seenHits[key] = struct{}{}
		result.Hits = append(result.Hits, capability.KnowledgeSearchHit{
			KnowledgeID: datasetID, DocumentID: documentID, ChunkID: chunkID,
			Text: text, Score: hit.Score, Title: strings.TrimSpace(hit.Title),
		})
	}
	if droppedUnmapped > 0 {
		log.Logger.Warn().Int("dropped_hits", droppedUnmapped).Int("dataset_count", len(datasetIDs)).
			Msg("knowledge search hits dropped because document mappings were not found")
	}
	return result, nil
}
