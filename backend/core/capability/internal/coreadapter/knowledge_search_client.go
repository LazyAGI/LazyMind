package coreadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"lazymind/core/capability"
	"lazymind/core/common"
)

const (
	pureKnowledgeSearchPath    = "/internal/knowledge:search"
	knowledgeSearchMaxResponse = 4 << 20
	internalServiceTokenHeader = "X-LazyMind-Internal-Token"
)

type KnowledgeSearchBackendRequest struct {
	UserID    string
	Query     string
	KBIDs     []string
	TopK      int
	LLMConfig map[string]any
}

type KnowledgeSearchBackendHit struct {
	KBID    string
	DocID   string
	ChunkID string
	Text    string
	Score   float64
	Title   string
}

type KnowledgeSearchBackendResponse struct{ Hits []KnowledgeSearchBackendHit }

type KnowledgeSearchBackendClient interface {
	Search(context.Context, KnowledgeSearchBackendRequest) (KnowledgeSearchBackendResponse, error)
}

type HTTPKnowledgeSearchBackendClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewHTTPKnowledgeSearchBackendClient(baseURL, token string, httpClient *http.Client) (*HTTPKnowledgeSearchBackendClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, capability.NewError(capability.Unsupported, "knowledge.search.client.new", "knowledge search endpoint is required", false, nil)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, capability.NewError(capability.Unsupported, "knowledge.search.client.new", "internal service token is required", false, nil)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HTTPKnowledgeSearchBackendClient{baseURL: baseURL, token: token, httpClient: httpClient}, nil
}

func (c *HTTPKnowledgeSearchBackendClient) Search(ctx context.Context, input KnowledgeSearchBackendRequest) (KnowledgeSearchBackendResponse, error) {
	payload, err := json.Marshal(map[string]any{
		"user_id": input.UserID, "query": input.Query, "kb_ids": input.KBIDs,
		"top_k": input.TopK, "llm_config": input.LLMConfig,
	})
	if err != nil {
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.Internal, "knowledge.search.client", "encode search request failed", false, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(c.baseURL, pureKnowledgeSearchPath), bytes.NewReader(payload))
	if err != nil {
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.Internal, "knowledge.search.client", "build search request failed", false, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(internalServiceTokenHeader, c.token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		if mapped := mapContextError("knowledge.search.client", err); mapped != nil {
			return KnowledgeSearchBackendResponse{}, mapped
		}
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.Unavailable, "knowledge.search.client", "knowledge search backend unavailable", true, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, knowledgeSearchMaxResponse+1))
	if err != nil {
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.Unavailable, "knowledge.search.client", "read search response failed", true, err)
	}
	if len(body) > knowledgeSearchMaxResponse {
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.ResultTooLarge, "knowledge.search.client", "search response exceeds 4 MiB", false, nil)
	}
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnprocessableEntity {
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.InvalidArgument, "knowledge.search.client", "invalid search request", false, nil)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.Unavailable, "knowledge.search.client", "knowledge search backend unavailable", true, nil)
	}
	var decoded struct {
		Hits []struct {
			KBID    string  `json:"kb_id"`
			DocID   string  `json:"doc_id"`
			ChunkID string  `json:"chunk_id"`
			Text    string  `json:"text"`
			Score   float64 `json:"score"`
			Title   string  `json:"title"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return KnowledgeSearchBackendResponse{}, capability.NewError(capability.Unavailable, "knowledge.search.client", "decode search response failed", true, err)
	}
	result := KnowledgeSearchBackendResponse{Hits: make([]KnowledgeSearchBackendHit, 0, len(decoded.Hits))}
	for _, hit := range decoded.Hits {
		result.Hits = append(result.Hits, KnowledgeSearchBackendHit{
			KBID: hit.KBID, DocID: hit.DocID, ChunkID: hit.ChunkID,
			Text: hit.Text, Score: hit.Score, Title: hit.Title,
		})
	}
	return result, nil
}
