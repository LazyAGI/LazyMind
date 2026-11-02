package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestUpstreamRecognitionResolvesCatalogTypesWithoutGuessingNames(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{
		Providers: map[string][]upstreamCatalogModel{
			"Qwen": {
				{Name: "Qwen/Qwen3-Reranker-8B", ModelType: "rerank"},
				{Name: "scoped-choice", ModelType: "llm"},
			},
		},
		RecognitionModels: []upstreamCatalogModel{
			{Name: "stackit/stackit-embedding-v2", ModelType: "embed"},
			{Name: "scoped-choice", ModelType: "embed"},
			{Name: "VendorA/shared-ranker", ModelType: "rerank"},
			{Name: "VendorB/shared-ranker", ModelType: "rerank"},
			{Name: "VendorC/shared-ranker", ModelType: ""},
			{Name: "vendor/versioned-v1:free", ModelType: "text2image"},
		},
	})
	for _, tc := range []struct {
		name, provider, id, want string
	}{
		{"scoped exact", "Qwen", "Qwen/Qwen3-Reranker-8B", "rerank"},
		{"global exact", "Custom", "stackit/stackit-embedding-v2", "embed"},
		{"case and whitespace", " qWen ", "  QWEN/QWEN3-RERANKER-8B  ", "rerank"},
		{"unprefixed ID", "Custom", "Qwen3-Reranker-8B", "rerank"},
		{"gateway prefix", "Custom", "gateway/Qwen/Qwen3-Reranker-8B", "rerank"},
		{"supplier metadata first", "Qwen", "scoped-choice", "llm"},
		{"same type across sources", "Custom", "shared-ranker", "rerank"},
		{"preserve full suffix", "Custom", "versioned-v1:free", "text2image"},
		{"do not remove free suffix", "Custom", "versioned-v1", ""},
		{"do not change version", "Custom", "versioned-v2:free", ""},
		{"do not change parameters", "Custom", "Qwen3-Reranker-4B", ""},
		{"unknown despite familiar name", "Custom", "gpt-unknown-chat-embedding", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := index.modelType(tc.provider, tc.id); got != tc.want {
				t.Fatalf("modelType(%q, %q) = %q, want %q", tc.provider, tc.id, got, tc.want)
			}
		})
	}
}

func TestUpstreamRecognitionRejectsConflictsAtEachLookupLevel(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{
		Providers: map[string][]upstreamCatalogModel{
			"Qwen": {
				{Name: "scoped-conflict", ModelType: "rerank"},
				{Name: "SCOPED-CONFLICT", ModelType: "embed"},
			},
		},
		RecognitionModels: []upstreamCatalogModel{
			{Name: "exact-conflict", ModelType: "tts"},
			{Name: "exact-conflict", ModelType: "llm"},
			{Name: "VendorA/basename-conflict", ModelType: "rerank"},
			{Name: "VendorB/basename-conflict", ModelType: "embed"},
		},
	})
	for _, tc := range []struct{ provider, id string }{
		{"Qwen", "scoped-conflict"},
		{"Custom", "exact-conflict"},
		{"Custom", "basename-conflict"},
	} {
		if got := index.modelType(tc.provider, tc.id); got != "" {
			t.Errorf("conflicting %q classified as %q", tc.id, got)
		}
	}
	// A conflicting basename must not invalidate an unambiguous exact ID.
	if got := index.modelType("Custom", "VendorA/basename-conflict"); got != "rerank" {
		t.Fatalf("exact ID = %q, want rerank", got)
	}
}

func TestUpstreamRecognitionUnknownEntriesBlockBroaderMatches(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{
		Providers: map[string][]upstreamCatalogModel{
			"OpenRouter": {
				{Name: "openai/gpt-audio", Input: []string{"text", "audio"}, Output: []string{"text", "audio"}},
			},
			"OpenAI": {
				{Name: "gpt-audio", ModelType: "llm"},
			},
		},
		RecognitionModels: []upstreamCatalogModel{
			{Name: "exact-unknown"},
			{Name: "vendor/exact-unknown", ModelType: "embed"},
			{Name: "openai/gpt-audio", ModelType: "llm"},
		},
	})
	for _, tc := range []struct{ provider, id string }{
		{"OpenRouter", "openai/gpt-audio"},
		{"Custom", "exact-unknown"},
	} {
		if got := index.modelType(tc.provider, tc.id); got != "" {
			t.Errorf("explicitly unknown %s/%s widened to %q", tc.provider, tc.id, got)
		}
	}
	// The scoped unknown blocks an otherwise unambiguous global exact match.
	if got := index.modelType("Custom", "openai/gpt-audio"); got != "llm" {
		t.Fatalf("known global exact type=%q, want llm", got)
	}
}

func TestUpstreamRecognitionUnknownAndKnownTypesAreOrderIndependent(t *testing.T) {
	unknown := upstreamCatalogModel{Name: "same-model"}
	chat := upstreamCatalogModel{Name: "same-model", ModelType: "llm"}
	embed := upstreamCatalogModel{Name: "same-model", ModelType: "embed"}
	for _, tc := range []struct {
		name   string
		models []upstreamCatalogModel
		want   string
	}{
		{"unknown then known", []upstreamCatalogModel{unknown, chat}, "llm"},
		{"known then unknown", []upstreamCatalogModel{chat, unknown}, "llm"},
		{"unknown between same types", []upstreamCatalogModel{chat, unknown, chat}, "llm"},
		{"unknown after conflict", []upstreamCatalogModel{chat, embed, unknown, chat}, ""},
		{"unknown before conflict", []upstreamCatalogModel{unknown, embed, chat}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{Providers: map[string][]upstreamCatalogModel{
				"Qwen": tc.models,
			}})
			for _, request := range []struct{ provider, id string }{
				{"Qwen", "same-model"},
				{"Custom", "same-model"},
				{"Custom", "gateway/same-model"},
			} {
				if got := index.modelType(request.provider, request.id); got != tc.want {
					t.Errorf("modelType(%s,%s)=%q, want %q", request.provider, request.id, got, tc.want)
				}
			}
		})
	}
}

func TestRemoteCatalogCompletionPreservesRemoteEvidence(t *testing.T) {
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{
		RecognitionModels: []upstreamCatalogModel{
			{Name: "catalog-chat", ModelType: "llm", Input: []string{"text"}, Output: []string{"text"}, Context: 128000},
			{Name: "catalog-ranker", ModelType: "rerank"},
			{Name: "catalog-vector", ModelType: "embed"},
			{Name: "qwen-plus", ModelType: "embed"},
		},
	})
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"ID only", `{"id":"catalog-ranker"}`, "rerank"},
		{"input only", `{"id":"catalog-ranker","input_modalities":["text"]}`, "rerank"},
		{"remote embedding input refines catalog", `{"id":"catalog-vector","input_modalities":["image"]}`, "multimodal_embedding"},
		{"explicit model type", `{"id":"catalog-ranker","model_type":"tts"}`, "tts"},
		{"explicit task", `{"id":"catalog-chat","task":"rerank"}`, "rerank"},
		{"unknown task", `{"id":"catalog-chat","task":"future-task"}`, "future-task"},
		{"unknown model type", `{"id":"qwen-plus","model_type":"decisions"}`, "decisions"},
		{"raw output overrides catalog", `{"id":"catalog-ranker","output_modalities":["image","text"]}`, "text2image"},
		{"raw output overrides declared", `{"id":"catalog-chat","model_type":"llm","output_modalities":["embeddings"]}`, "embed"},
		{"unknown output blocks catalog", `{"id":"catalog-chat","output_modalities":["decisions"]}`, ""},
		{"mixed audio blocks catalog", `{"id":"catalog-chat","output_modalities":["text","audio"]}`, ""},
		{"unknown output blocks declared", `{"id":"catalog-chat","model_type":"llm","output_modalities":["decisions"]}`, ""},
		{"unknown legacy name stays unknown", `{"id":"qwen-plus","task":"future-task"}`, "future-task"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entry openaiModelListEntry
			if err := json.Unmarshal([]byte(tc.raw), &entry); err != nil {
				t.Fatal(err)
			}
			for _, provider := range []string{"Custom", "OpenRouter"} {
				got := remoteModelItem(entry, provider, index)
				if got.ModelType != tc.want {
					t.Errorf("provider %s type=%q, want %q", provider, got.ModelType, tc.want)
				}
			}
		})
	}
	got := remoteModelItem(openaiModelListEntry{ID: "catalog-chat", ContextLength: 123456}, "OpenRouter", index)
	if len(got.Input) != 0 || len(got.Output) != 0 || got.Input == nil || got.Output == nil {
		t.Fatalf("catalog fabricated raw modalities: %+v", got)
	}
	if got.MaxInputTokens == nil || *got.MaxInputTokens != "123456" {
		t.Fatalf("remote context was replaced: %+v", got)
	}
}

func TestRemoteCompletionUsesOnlyTypedLegacyTokenLimits(t *testing.T) {
	contextWindowsMu.RLock()
	previousByType, previousOrder := contextWindowsByType, contextWindowTypeOrder
	contextWindowsMu.RUnlock()
	t.Cleanup(func() {
		contextWindowsMu.Lock()
		contextWindowsByType, contextWindowTypeOrder = previousByType, previousOrder
		contextWindowsMu.Unlock()
	})
	path := filepath.Join(t.TempDir(), "windows.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  qwen-plus: 128K\nembed:\n  catalog-vector: 8192\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadContextWindows(path); err != nil {
		t.Fatal(err)
	}
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{RecognitionModels: []upstreamCatalogModel{
		{Name: "qwen-plus", ModelType: "llm"},
		{Name: "qwen-plus", ModelType: "embed"},
		{Name: "catalog-vector", ModelType: "embed"},
	}})
	for _, entry := range []openaiModelListEntry{
		{ID: "qwen-plus"},
		{ID: "qwen-plus", ModelType: "decisions"},
		{ID: "qwen-plus", OutputModalities: []string{"decisions"}},
	} {
		if got := remoteModelItem(entry, "Custom", index); got.MaxInputTokens != nil {
			t.Errorf("unclassified model inherited legacy tokens: %+v", got)
		}
	}
	got := remoteModelItem(openaiModelListEntry{ID: "catalog-vector"}, "Custom", index)
	if got.MaxInputTokens == nil || *got.MaxInputTokens != "8192" {
		t.Fatalf("typed embedding lost its known limit: %+v", got)
	}
}

func TestLoadUpstreamRecognitionReadsSchemaThreeWithoutSeedingProviders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":3,"providers":{"OpenAI":[]},"recognition_models":[{"name":"stackit-embedding-v2","model_type":"embed"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	index, err := loadUpstreamModelRecognitionIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := index.modelType("Custom", "STACKIT-EMBEDDING-V2"); got != "embed" {
		t.Fatalf("schema3 recognition type=%q, want embed", got)
	}
	if _, err := loadUpstreamModelRecognitionIndex(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing catalog should fail to load")
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":3,"providers":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadUpstreamModelRecognitionIndex(path); err == nil {
		t.Fatal("malformed catalog should fail to load")
	}
}

func TestRemoteDiscoveryCompletesIDOnlyModelsFromInjectedCatalog(t *testing.T) {
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false; store.Init(nil, nil, nil) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"Qwen/Qwen3-Reranker-8B"},{"id":"stackit-embedding-v2"},{"id":"catalog-chat","output_modalities":["decisions"]},{"id":"catalog-future","task":"future-task"},{"id":"openai/gpt-audio"}]}`))
	}))
	defer upstream.Close()
	db, parent, group := setupEncryptedGroupKeyTest(t, "secret")
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&parent).Updates(map[string]any{"protocol_name": "OpenRouter", "base_url": upstream.URL}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&group).Update("base_url", upstream.URL).Error; err != nil {
		t.Fatal(err)
	}
	index := newUpstreamModelRecognitionIndex(upstreamCatalogFile{
		Providers: map[string][]upstreamCatalogModel{
			"OpenRouter": {{Name: "openai/gpt-audio", Output: []string{"text", "audio"}}},
			"OpenAI":     {{Name: "gpt-audio", ModelType: "llm"}},
		},
		RecognitionModels: []upstreamCatalogModel{
			{Name: "Qwen/Qwen3-Reranker-8B", ModelType: "rerank"},
			{Name: "stackit-embedding-v2", ModelType: "embed"},
			{Name: "catalog-chat", ModelType: "llm"},
			{Name: "catalog-future", ModelType: "llm"},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/remote_models", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	listRemoteGroupModels(rec, req, index)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data remoteGroupModelsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Qwen/Qwen3-Reranker-8B": "rerank", "stackit-embedding-v2": "embed", "catalog-chat": "", "catalog-future": "future-task", "openai/gpt-audio": ""}
	if len(body.Data.Models) != len(want) {
		t.Fatalf("unexpected models: %+v", body.Data.Models)
	}
	for _, model := range body.Data.Models {
		if model.ModelType != want[model.ID] {
			t.Errorf("%s type=%q, want %q", model.ID, model.ModelType, want[model.ID])
		}
		if model.Input == nil || model.Output == nil {
			t.Errorf("raw modalities must be arrays: %+v", model)
		}
		if model.ID == "catalog-chat" && !reflect.DeepEqual(model.Output, []string{"decisions"}) {
			t.Errorf("lost raw output: %+v", model)
		}
		if model.ID != "catalog-chat" && (len(model.Input) != 0 || len(model.Output) != 0) {
			t.Errorf("fabricated raw modalities: %+v", model)
		}
	}
}
