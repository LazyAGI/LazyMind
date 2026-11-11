package coreadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
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

func TestRetrievalModelReadinessBeforeKnowledgeSearch(t *testing.T) {
	for _, tc := range []struct {
		name          string
		static        bool
		roleStatus    int
		embedding     *orm.EmbeddingIndexState
		embeddingName string
		wantReason    string
	}{
		{name: "dynamic with only a chat model", wantReason: "model_unavailable"},
		{name: "static with no injected models", static: true},
		{name: "missing runtime role", roleStatus: http.StatusNotFound, wantReason: "runtime_role_missing"},
		{
			name: "static with unsigned index", static: true, embeddingName: "embedding",
			embedding: &orm.EmbeddingIndexState{Phase: "ready"}, wantReason: "model_unavailable",
		},
		{
			name: "dynamic during index cleanup", embeddingName: "embedding",
			embedding: &orm.EmbeddingIndexState{Phase: "clearing"}, wantReason: "model_unavailable",
		},
		{
			name:       "ready dynamic selection without injectable runtime configuration",
			embedding:  &orm.EmbeddingIndexState{Phase: "ready", Signature: "v1:1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)},
			wantReason: "runtime_config_missing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := "/" + uuid.NewString()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != prefix+"/api/model/role_type" || r.URL.Query().Get("role") != "embed_main" {
					t.Errorf("unexpected role request: %s", r.URL)
					http.NotFound(w, r)
					return
				}
				if tc.roleStatus != 0 {
					w.WriteHeader(tc.roleStatus)
					_, _ = w.Write([]byte(`{"detail":"private-role-response"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"type": "embed", "is_dynamic": !tc.static})
			}))
			t.Cleanup(server.Close)
			// Role metadata is cached by endpoint, so each case needs a unique URL.
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL+prefix)

			db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{},
				&orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{},
				&orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.DocumentProcessingState{}).DB
			base := orm.BaseModel{CreateUserID: "user-1"}
			rows := []any{
				&orm.UserModelProvider{ID: "provider", Name: "OpenAI", ProtocolName: "OpenAI", Capabilities: "has_models", BaseModel: base},
				&orm.UserModelProviderGroup{ID: "group", UserModelProviderID: "provider", BaseURL: "https://example.com/v1", APIKey: "private-test-key", IsVerified: true, BaseModel: base},
			}
			if !tc.static {
				rows = append(rows,
					&orm.UserModelProviderGroupModel{ID: "chat", UserModelProviderID: "provider", UserModelProviderGroupID: "group", ProviderName: "OpenAI", Name: "chat-model", ModelType: "llm", BaseModel: base},
					&orm.UserSelectedModel{UserID: "user-1", ModelKey: "llm", UserModelProviderGroupModelID: "chat"},
				)
			}
			if tc.embedding != nil {
				state := *tc.embedding
				state.Scope, state.ActiveSource, state.ActiveModelID = "global", "own", "embedding"
				rows = append(rows,
					&orm.UserModelProviderGroupModel{ID: "embedding", UserModelProviderID: "provider", UserModelProviderGroupID: "group", ProviderName: "OpenAI", Name: tc.embeddingName, ModelType: "embed", BaseModel: base},
					&state,
				)
			}
			for _, row := range rows {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			models, err := NewDBBackedRetrievalModelConfigLoader(db)
			if err != nil {
				t.Fatal(err)
			}
			scope := &fakeKnowledgeScopeResolver{scope: KnowledgeScope{DatasetIDToKBID: map[string]string{"ds-1": "kb-1"}}}
			client := &fakeKnowledgeSearchBackendClient{}
			searcher, err := NewKnowledgeSearcher(scope, client, &fakeDocumentIDMapper{}, models)
			if err != nil {
				t.Fatal(err)
			}
			_, err = searcher.SearchKnowledge(t.Context(), capability.InvocationContext{Principal: capability.Principal{UserID: "user-1"}}, capability.SearchKnowledgeInput{
				Query: "needle", KnowledgeIDs: []string{"ds-1"}, TopK: 5,
			})
			if tc.wantReason == "" {
				if err != nil || client.request.Query != "needle" || len(client.request.LLMConfig) != 0 {
					t.Fatalf("static search: backend query=%q model roles=%d err=%v", client.request.Query, len(client.request.LLMConfig), err)
				}
				return
			}
			if client.request.Query != "" {
				t.Error("search reached the algorithm backend with an unavailable embedding")
			}
			assertEmbeddingNotReady(t, err, tc.wantReason)
		})
	}
}

func TestHTTPKnowledgeSearchClientMapsEmbeddingNotReady(t *testing.T) {
	const typedBody = `{"detail":{"code":"EMBEDDING_NOT_READY","message":"private-test-key https://example.com?signature=private-signature","api_key":"private-backend-field"}}`
	for _, tc := range []struct {
		name, body string
		status     int
		code       capability.ErrorCode
	}{
		{"typed embedding error", typedBody, http.StatusUnprocessableEntity, capability.Unavailable},
		{"other validation error", `{"detail":{"code":"OTHER","message":"private-test-key"}}`, http.StatusUnprocessableEntity, capability.InvalidArgument},
		{"FastAPI validation array", `{"detail":[{"msg":"private-test-key"}]}`, http.StatusUnprocessableEntity, capability.InvalidArgument},
		{"typed error on HTTP 400", typedBody, http.StatusBadRequest, capability.InvalidArgument},
		{"oversized typed error", typedBody + strings.Repeat(" ", knowledgeSearchMaxResponse), http.StatusUnprocessableEntity, capability.ResultTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Request: request}, nil
			})}
			client, err := NewHTTPKnowledgeSearchBackendClient("http://chat.test", "internal-token", httpClient)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Search(t.Context(), KnowledgeSearchBackendRequest{})
			if tc.code == capability.Unavailable {
				assertEmbeddingNotReady(t, err, "algorithm_runtime_unavailable")
				return
			}
			if code, _ := capability.CodeOf(err); code != tc.code {
				t.Fatalf("error code=%s, want %s", code, tc.code)
			}
		})
	}
}

func assertEmbeddingNotReady(t *testing.T, err error, readinessReason string) {
	t.Helper()
	var typed *capability.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected a capability error, got %T", err)
	}
	if typed.Code != capability.Unavailable || typed.Reason != "EMBEDDING_NOT_READY" || typed.Retryable {
		t.Fatalf("code=%s reason=%s retryable=%v", typed.Code, typed.Reason, typed.Retryable)
	}
	if typed.Details["required_model_role"] != "embed_main" || typed.Details["recovery"] != "open_model_defaults" ||
		typed.Details["readiness_reason"] != readinessReason {
		t.Fatalf("unexpected readiness details: %#v", typed.Details)
	}
	if typed.Action == nil || typed.Action.Type != "configure_model" || typed.Action.URL != "/settings?section=model_defaults&target=embed_main" {
		t.Fatalf("unexpected recovery action: %#v", typed.Action)
	}
	encoded, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-test-key", "private-signature", "private-backend-field", "private-role-response", "api_key"} {
		if strings.Contains(string(encoded), private) || strings.Contains(typed.Error(), private) {
			t.Fatal("embedding readiness error exposed private backend information")
		}
	}
	if errors.Unwrap(typed) != nil {
		t.Fatal("embedding readiness error retained a raw backend cause")
	}
}
