package coreadapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"lazymind/core/capability"
)

type fakeKnowledgeScopeResolver struct {
	scope KnowledgeScope
	ids   []string
}

func (r *fakeKnowledgeScopeResolver) ResolveKnowledge(_ context.Context, _, _ string, ids []string) (KnowledgeScope, error) {
	r.ids = append([]string(nil), ids...)
	return r.scope, nil
}

type fakeKnowledgeSearchBackendClient struct {
	request  KnowledgeSearchBackendRequest
	response KnowledgeSearchBackendResponse
}

func (c *fakeKnowledgeSearchBackendClient) Search(_ context.Context, request KnowledgeSearchBackendRequest) (KnowledgeSearchBackendResponse, error) {
	c.request = request
	return c.response, nil
}

type fakeRetrievalModelConfigLoader struct{ config map[string]any }

func (l *fakeRetrievalModelConfigLoader) LoadRetrievalModelConfig(context.Context, string) (map[string]any, error) {
	return l.config, nil
}

type fakeDocumentIDMapper struct{ mapping map[documentMapKey]string }

func (m *fakeDocumentIDMapper) MapCoreDocumentIDs(context.Context, []string, []string) (map[documentMapKey]string, error) {
	return m.mapping, nil
}

func TestKnowledgeSearcherMapsOnlyAuthorizedTextHits(t *testing.T) {
	scope := &fakeKnowledgeScopeResolver{scope: KnowledgeScope{
		DatasetIDToKBID: map[string]string{"ds-1": "kb-1"}, KBIDToDatasetID: map[string]string{"kb-1": "ds-1"},
	}}
	client := &fakeKnowledgeSearchBackendClient{response: KnowledgeSearchBackendResponse{Hits: []KnowledgeSearchBackendHit{
		{KBID: "kb-1", DocID: "lazy-1", ChunkID: "chunk-1", Text: "result", Score: .9},
		{KBID: "kb-other", DocID: "lazy-1", Text: "escaped"},
	}}}
	mapper := &fakeDocumentIDMapper{mapping: map[documentMapKey]string{{DatasetID: "ds-1", LazyDocID: "lazy-1"}: "doc-1"}}
	models := &fakeRetrievalModelConfigLoader{config: map[string]any{"embed_main": map[string]any{"model": "embed"}}}
	searcher, err := NewKnowledgeSearcher(scope, client, mapper, models)
	if err != nil {
		t.Fatal(err)
	}
	result, err := searcher.SearchKnowledge(context.Background(), capability.InvocationContext{Principal: capability.Principal{UserID: "user-1"}}, capability.SearchKnowledgeInput{
		Query: "query", KnowledgeIDs: []string{"ds-1"}, TopK: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.request.UserID != "user-1" || strings.Join(client.request.KBIDs, ",") != "kb-1" || client.request.TopK != 7 || client.request.LLMConfig["embed_main"] == nil {
		t.Fatalf("backend request = %#v", client.request)
	}
	if len(result.Hits) != 1 || result.Hits[0].DocumentID != "doc-1" {
		t.Fatalf("mapped result = %#v", result)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestHTTPKnowledgeSearchClientUsesPureRouteAndForwardsModels(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://chat.test/internal/knowledge:search" {
			t.Fatalf("URL = %s", request.URL)
		}
		if got := request.Header.Get(internalServiceTokenHeader); got != "internal-token" {
			t.Fatalf("internal token = %q", got)
		}
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["user_id"] != "user-1" || payload["query"] != "needle" || payload["llm_config"].(map[string]any)["embed_main"] == nil {
			t.Fatalf("unexpected pure retrieval payload: %s", body)
		}
		for _, forbidden := range []string{"answer", "conversation", "history", "prompt"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("pure retrieval payload contains %q: %s", forbidden, body)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"hits":[{"score":0.8,"chunk_id":"chunk","doc_id":"lazy-doc","kb_id":"kb","text":"hit","title":"Document"}]}`)),
			Request: request,
		}, nil
	})}
	client, err := NewHTTPKnowledgeSearchBackendClient("http://chat.test", "internal-token", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Search(context.Background(), KnowledgeSearchBackendRequest{
		UserID: "user-1", Query: "needle", KBIDs: []string{"kb"}, TopK: 5,
		LLMConfig: map[string]any{"embed_main": map[string]any{"model": "embed"}},
	})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].Text != "hit" || result.Hits[0].DocID != "lazy-doc" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestFilterRetrievalModelConfigAllowsOnlyExistingSearchRoles(t *testing.T) {
	input := map[string]any{
		"llm":         map[string]any{"model": "query-enhancer"},
		"embed_main":  map[string]any{"model": "embedding"},
		"reranker":    map[string]any{"model": "reranker"},
		"embed_image": map[string]any{"model": "image-embedding"},
		"evo_llm":     map[string]any{"model": "must-not-leave-core"},
		"vlm":         map[string]any{"model": "must-not-leave-core"},
	}

	filtered := filterRetrievalModelConfig(input)
	if len(filtered) != 4 || filtered["llm"] == nil || filtered["embed_main"] == nil || filtered["reranker"] == nil || filtered["embed_image"] == nil {
		t.Fatalf("filtered retrieval config = %#v", filtered)
	}
	if filtered["evo_llm"] != nil || filtered["vlm"] != nil {
		t.Fatalf("non-retrieval roles leaked: %#v", filtered)
	}
}
