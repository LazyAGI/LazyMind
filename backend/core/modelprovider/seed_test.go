package modelprovider

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestModelCatalogIncludesOpenRouter(t *testing.T) {
	yamlBytes, err := os.ReadFile("../config/model_catalog.yaml")
	if err != nil {
		t.Fatalf("read model catalog: %v", err)
	}
	catalog, err := loadModelCatalog(yamlBytes)
	if err != nil {
		t.Fatalf("load model catalog: %v", err)
	}

	for _, supplier := range catalog["model_providers"].Suppliers {
		if supplier.Name != "OpenRouter" {
			continue
		}
		if supplier.BaseURL != "https://openrouter.ai/api/v1/" {
			t.Fatalf("unexpected OpenRouter base URL: %q", supplier.BaseURL)
		}
		modelsByName := make(map[string]catalogModel, len(supplier.Models))
		for _, model := range supplier.Models {
			modelsByName[model.Name] = model
		}
		expectedTypes := map[string]string{
			"z-ai/glm-5.3-flash":                    "llm",
			"z-ai/glm-5.3":                          "llm",
			"deepseek/deepseek-v4-pro":              "llm",
			"nvidia/nemotron-3.5-lightning:free":    "llm",
			"z-ai/glm-5.2:free":                     "llm",
			"openrouter/free":                       "vlm",
			"openrouter/auto":                       "vlm",
			"openai/gpt-5.6-sol":                    "vlm",
			"anthropic/claude-sonnet-5":             "vlm",
			"google/gemini-3.7-flash":               "vlm",
			"x-ai/grok-4.6":                         "vlm",
			"qwen/qwen3.8-max":                      "vlm",
			"qwen/qwen3.8-flash":                    "vlm",
			"moonshotai/kimi-k3":                    "vlm",
			"minimax/minimax-m3":                    "vlm",
			"minimax/minimax-m3:free":               "vlm",
			"deepseek/deepseek-v4-flash-vision-exp": "vlm",
			"thinkingmachines/inkling:free":         "vlm",
			"liquid/lfm-2.5-embedding-350m:free":    "embed",
			"bytedance-seed/seedream-4.5":           "text2image",
			"bytedance-seed/seedream-5-0-lite":      "text2image",
			"bytedance-seed/seedream-5-0-pro":       "text2image",
			"x-ai/grok-imagine-image-2.0":           "text2image",
			"qwen/qwen-image-3-pro":                 "text2image",
			"openai/gpt-image-2":                    "text2image",
			"alibaba/wan-3.0":                       "text2video",
			"bytedance/seedance-2.0-mini":           "text2video",
			"bytedance/seedance-2.5":                "text2video",
			"black-forest-labs/flux-3-video":        "text2video",
			"runway/gen-4.5":                        "text2video",
			"deepgram/flux-tts:free":                "tts",
			"fish-audio/s2.1-pro":                   "tts",
			"microsoft/mai-voice-2-flash":           "tts",
			"qwen/qwen-audio-3.0-tts-flash":         "tts",
			"mistralai/voxtral-mini-tts-2603":       "tts",
			"openai/whisper-large-v3":               "stt",
			"openai/whisper-large-v3-turbo":         "stt",
			"mistralai/voxtral-small-24b-2507-stt":  "stt",
			"qwen/qwen3-asr-1.7b":                   "stt",
			"deepgram/nova-3":                       "stt",
		}
		if len(modelsByName) != len(expectedTypes) {
			t.Fatalf("unexpected OpenRouter model count: got %d, want %d", len(modelsByName), len(expectedTypes))
		}
		for name, modelType := range expectedTypes {
			model, ok := modelsByName[name]
			if !ok || model.Type != modelType {
				t.Fatalf("unexpected OpenRouter model %q: %+v", name, model)
			}
		}
		freeModel := modelsByName["z-ai/glm-5.3-flash"]
		if freeModel.Type != "llm" || freeModel.MaxInputTokens == nil || *freeModel.MaxInputTokens != "1310720" || freeModel.FreeAutoSelectPriority != 1 {
			t.Fatalf("unexpected free OpenRouter model config: %+v", freeModel)
		}
		freeVLM := modelsByName["openrouter/free"]
		if freeVLM.Type != "vlm" || freeVLM.MaxInputTokens == nil || *freeVLM.MaxInputTokens != "200K" || freeVLM.FreeAutoSelectPriority != 1 {
			t.Fatalf("unexpected free OpenRouter VLM config: %+v", freeVLM)
		}
		if modelsByName["liquid/lfm-2.5-embedding-350m:free"].FreeAutoSelectPriority != 1 {
			t.Fatalf("unexpected free OpenRouter embedding config: %+v", modelsByName["liquid/lfm-2.5-embedding-350m:free"])
		}
		return
	}
	t.Fatal("OpenRouter provider is missing from model catalog")
}

func TestModelCatalogIncludesCurrentSenseNovaModels(t *testing.T) {
	yamlBytes, err := os.ReadFile("../config/model_catalog.yaml")
	if err != nil {
		t.Fatalf("read model catalog: %v", err)
	}
	catalog, err := loadModelCatalog(yamlBytes)
	if err != nil {
		t.Fatalf("load model catalog: %v", err)
	}

	expectedTypes := map[string]string{
		"SenseChat-5":                  "llm",
		"DeepSeek-R1":                  "llm",
		"DeepSeek-R1-Distill-Qwen-14B": "llm",
		"DeepSeek-R1-Distill-Qwen-32B": "llm",
		"DeepSeek-V3":                  "llm",
		"Llama3-70B":                   "llm",
		"Llama3-8B":                    "llm",
		"Qwen2-72B":                    "llm",
		"Qwen2-7B":                     "llm",
		"Qwen3-235B":                   "llm",
		"Qwen3-32B":                    "llm",
		"SenseChat":                    "llm",
		"SenseChat-128K":               "llm",
		"SenseChat-32K":                "llm",
		"SenseChat-5-Cantonese":        "llm",
		"SenseChat-Character":          "llm",
		"SenseChat-Character-Pro":      "llm",
		"SenseChat-Turbo":              "llm",
		"SenseChat-Vision":             "vlm",
		"SenseNova-V6-5-Pro":           "vlm",
		"SenseNova-V6-5-Turbo":         "vlm",
		"SenseNova-V6-Pro":             "vlm",
		"SenseNova-V6-Reasoner":        "vlm",
		"SenseNova-V6-Turbo":           "vlm",
		"nova-embedding-stable":        "embed",
		"deepseek-v4-flash":            "llm",
		"glm-5.2":                      "llm",
		"sensenova-6.7-flash-lite":     "llm",
		"sensenova-u1-fast":            "text2image",
		"sensenova-u1.5-lite":          "image_editing",
	}

	for _, supplier := range catalog["model_providers"].Suppliers {
		if supplier.Name != "SenseNova" {
			continue
		}
		modelsByName := make(map[string]catalogModel, len(supplier.Models))
		for _, model := range supplier.Models {
			modelsByName[model.Name] = model
		}
		if len(modelsByName) != len(expectedTypes) {
			t.Fatalf("unexpected SenseNova model count: got %d, want %d", len(modelsByName), len(expectedTypes))
		}
		for name, modelType := range expectedTypes {
			model, ok := modelsByName[name]
			if !ok || model.Type != modelType {
				t.Fatalf("unexpected SenseNova model %q: %+v", name, model)
			}
		}
		return
	}
	t.Fatal("SenseNova provider is missing from model catalog")
}

func TestReconcileSenseNovaCatalogScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:sensenova_catalog_scope?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.DefaultModel{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserModelProviderGroupModel{},
		&orm.UserSelectedModel{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	if err := db.Create(&[]orm.DefaultModel{
		{ID: "default-classic", DefaultModelProviderID: "provider", ProviderName: "SenseNova", Name: "SenseChat-5", ModelType: "llm", CreatedAt: now, UpdatedAt: now},
		{ID: "default-token", DefaultModelProviderID: "provider", ProviderName: "SenseNova", Name: "sensenova-6.7-flash-lite", ModelType: "llm", CreatedAt: now, UpdatedAt: now},
		{ID: "default-retired", DefaultModelProviderID: "provider", ProviderName: "SenseNova", Name: "SenseChat-5-1202", ModelType: "llm", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create default models: %v", err)
	}
	if err := db.Create(&orm.UserModelProvider{
		ID: "user-provider", DefaultModelProviderID: "provider", Name: "SenseNova",
		BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create user provider: %v", err)
	}
	groups := []orm.UserModelProviderGroup{
		{ID: "classic", UserModelProviderID: "user-provider", BaseURL: "https://api.sensenova.cn/compatible-mode/v1/", BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
		{ID: "token", UserModelProviderID: "user-provider", BaseURL: sensenovaNewPlatformBaseURL, BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
		{ID: "custom", UserModelProviderID: "user-provider", BaseURL: "https://example.com/v1/", BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatalf("create groups: %v", err)
	}
	groupModels := []orm.UserModelProviderGroupModel{
		{ID: "classic-current", UserModelProviderID: "user-provider", UserModelProviderGroupID: "classic", ProviderName: "SenseNova", Name: "SenseChat-5", ModelType: "llm", IsDefault: true, BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
		{ID: "classic-token", UserModelProviderID: "user-provider", UserModelProviderGroupID: "classic", ProviderName: "SenseNova", Name: "sensenova-6.7-flash-lite", ModelType: "llm", IsDefault: true, BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
		{ID: "classic-retired", UserModelProviderID: "user-provider", UserModelProviderGroupID: "classic", ProviderName: "SenseNova", Name: "SenseChat-5-1202", ModelType: "llm", IsDefault: true, BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
		{ID: "token-classic", UserModelProviderID: "user-provider", UserModelProviderGroupID: "token", ProviderName: "SenseNova", Name: "SenseChat-5", ModelType: "llm", IsDefault: true, BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
		{ID: "token-current", UserModelProviderID: "user-provider", UserModelProviderGroupID: "token", ProviderName: "SenseNova", Name: "sensenova-6.7-flash-lite", ModelType: "llm", IsDefault: true, BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
		{ID: "custom-retired", UserModelProviderID: "user-provider", UserModelProviderGroupID: "custom", ProviderName: "SenseNova", Name: "SenseChat-5-1202", ModelType: "llm", IsDefault: true, BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}},
	}
	if err := db.Create(&groupModels).Error; err != nil {
		t.Fatalf("create group models: %v", err)
	}
	if err := db.Create(&orm.UserSelectedModel{
		UserID: "user", ModelKey: "llm", UserModelProviderGroupModelID: "classic-retired",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create selection: %v", err)
	}

	err = reconcileSenseNovaCatalogScope(db, "provider", "https://api.sensenova.cn/compatible-mode/v1/", []catalogModel{
		{Name: "SenseChat-5", Type: "llm"},
		{Name: "sensenova-6.7-flash-lite", Type: "llm"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var remainingGroupModels []orm.UserModelProviderGroupModel
	if err := db.Order("id").Find(&remainingGroupModels).Error; err != nil {
		t.Fatalf("load group models: %v", err)
	}
	gotIDs := make([]string, 0, len(remainingGroupModels))
	for _, model := range remainingGroupModels {
		gotIDs = append(gotIDs, model.ID)
	}
	wantIDs := []string{"classic-current", "custom-retired", "token-current"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("remaining group model IDs = %v, want %v", gotIDs, wantIDs)
	}
	var selectionCount int64
	if err := db.Model(&orm.UserSelectedModel{}).Count(&selectionCount).Error; err != nil || selectionCount != 0 {
		t.Fatalf("selection count = %d, err = %v", selectionCount, err)
	}
	var defaultModelNames []string
	if err := db.Model(&orm.DefaultModel{}).Order("name").Pluck("name", &defaultModelNames).Error; err != nil {
		t.Fatalf("load default models: %v", err)
	}
	wantDefaultNames := []string{"SenseChat-5", "sensenova-6.7-flash-lite"}
	if !reflect.DeepEqual(defaultModelNames, wantDefaultNames) {
		t.Fatalf("default model names = %v, want %v", defaultModelNames, wantDefaultNames)
	}
}

// --- normalizeBaseURL ---

// TestNormalizeBaseURL_AddsTrailingSlash appends slash to plain domain.
func TestNormalizeBaseURL_AddsTrailingSlash(t *testing.T) {
	got := normalizeBaseURL("https://api.openai.com")
	want := "https://api.openai.com/"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestNormalizeBaseURL_HasTrailingSlash keeps it as-is.
func TestNormalizeBaseURL_HasTrailingSlash(t *testing.T) {
	got := normalizeBaseURL("https://api.openai.com/")
	want := "https://api.openai.com/"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestNormalizeBaseURL_EndpointPath skips adding slash for endpoint URLs.
func TestNormalizeBaseURL_EndpointPath(t *testing.T) {
	// "/embeddings", "/rerank", "/embed" in the URL → no trailing slash added.
	got := normalizeBaseURL("https://api.openai.com/v1/embeddings")
	want := "https://api.openai.com/v1/embeddings"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestNormalizeBaseURL_RerankEndpoint skips trailing slash for /rerank paths.
func TestNormalizeBaseURL_RerankEndpoint(t *testing.T) {
	got := normalizeBaseURL("https://api.cohere.com/v1/rerank")
	if !strings.Contains(got, "rerank") || strings.HasSuffix(got, "/") {
		t.Fatalf("got %q, expected no trailing slash on rerank endpoint", got)
	}
}

// TestNormalizeBaseURL_EmbedEndpoint skips trailing slash for /embed paths.
func TestNormalizeBaseURL_EmbedEndpoint(t *testing.T) {
	got := normalizeBaseURL("https://api.example.com/v1/embed")
	if !strings.Contains(got, "embed") || strings.HasSuffix(got, "/") {
		t.Fatalf("got %q, expected no trailing slash on embed endpoint", got)
	}
}

// TestNormalizeBaseURL_Empty returns empty.
func TestNormalizeBaseURL_Empty(t *testing.T) {
	got := normalizeBaseURL("")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestNormalizeBaseURL_WhitespaceOnly returns empty.
func TestNormalizeBaseURL_WhitespaceOnly(t *testing.T) {
	got := normalizeBaseURL("   ")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestNormalizeBaseURL_SubstringMatchWithEmbed blocks trailing slash because "/embed" is a marker.
func TestNormalizeBaseURL_SubstringMatchWithEmbed(t *testing.T) {
	// "/embed" in the URL is detected as an endpoint marker → no trailing slash added.
	got := normalizeBaseURL("https://api.example.com/embedding-service")
	want := "https://api.example.com/embedding-service"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// A URL without endpoint markers gets a trailing slash.
	got2 := normalizeBaseURL("https://api.example.com/some-path")
	want2 := "https://api.example.com/some-path/"
	if got2 != want2 {
		t.Fatalf("got %q, want %q", got2, want2)
	}
}

// --- loadModelCatalog ---

// TestLoadModelCatalog_EmptyYAML returns nil catalog when YAML is empty (no sections).
func TestLoadModelCatalog_EmptyYAML(t *testing.T) {
	catalog, err := loadModelCatalog([]byte(``))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if catalog != nil {
		t.Fatalf("expected nil catalog for empty YAML, got %v", catalog)
	}
}

// TestLoadModelCatalog_InvalidYAML returns error.
func TestLoadModelCatalog_InvalidYAML(t *testing.T) {
	_, err := loadModelCatalog([]byte(`: bad yaml`))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// TestLoadModelCatalog_MinimalProvider parses a simple model_providers section.
func TestLoadModelCatalog_MinimalProvider(t *testing.T) {
	yaml := []byte(`
model_providers:
  capabilities: [chat]
  suppliers:
    - name: OpenAI
      base_url: https://api.openai.com/v1
      models:
        - name: gpt-4
          type: llm
          free_auto_select_priority: 1
          free_auto_select_base_urls: [https://free.example.com/v1/]
`)
	catalog, err := loadModelCatalog(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	section, ok := catalog["model_providers"]
	if !ok {
		t.Fatal("expected model_providers section")
	}
	if len(section.Suppliers) != 1 || section.Suppliers[0].Name != "OpenAI" {
		t.Fatalf("unexpected suppliers: %+v", section.Suppliers)
	}
	if len(section.Suppliers[0].Models) != 1 || section.Suppliers[0].Models[0].Name != "gpt-4" {
		t.Fatalf("unexpected models: %+v", section.Suppliers[0].Models)
	}
	model := section.Suppliers[0].Models[0]
	if model.FreeAutoSelectPriority != 1 ||
		len(model.FreeAutoSelectBaseURLs) != 1 ||
		model.FreeAutoSelectBaseURLs[0] != "https://free.example.com/v1/" {
		t.Fatalf("unexpected free auto-selection metadata: %+v", model)
	}
}
