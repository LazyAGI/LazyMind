package modelprovider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestHardcodedModelCatalogDisabledByDefault(t *testing.T) {
	t.Setenv("LAZYMIND_HARDCODED_MODEL_CATALOG", "")
	if HardcodedModelCatalogEnabled() {
		t.Fatal("hardcoded catalog should stay disabled unless LAZYMIND_HARDCODED_MODEL_CATALOG=1")
	}
	t.Setenv("LAZYMIND_HARDCODED_MODEL_CATALOG", "1")
	if !HardcodedModelCatalogEnabled() {
		t.Fatal("expected hardcoded catalog to be enabled")
	}
}

func TestInferUpstreamCatalogModel(t *testing.T) {
	cases := []struct {
		name   string
		item   upstreamCatalogModel
		want   string
		vision bool
	}{
		{name: "chat", item: upstreamCatalogModel{Name: "demo-chat", Input: []string{"text"}, Output: []string{"text"}, Context: 128000}, want: "llm"},
		{name: "vision chat", item: upstreamCatalogModel{Name: "demo-vision", Input: []string{"text", "image"}, Output: []string{"text"}, Context: 1000000}, want: "llm", vision: true},
		{name: "explicit vision type", item: upstreamCatalogModel{Name: "demo-vision", ModelType: "vlm", Input: []string{"text", "image"}, Output: []string{"text"}}, want: "vlm", vision: true},
		{name: "embed", item: upstreamCatalogModel{Name: "demo-embed", Input: []string{"text"}, Output: []string{"text"}}, want: "embed"},
		{name: "rerank", item: upstreamCatalogModel{Name: "demo-rerank", Input: []string{"text"}, Output: []string{"text"}}, want: "rerank"},
		{name: "stt", item: upstreamCatalogModel{Name: "demo-stt", Input: []string{"audio"}, Output: []string{"text"}}, want: "stt"},
		{name: "tts", item: upstreamCatalogModel{Name: "demo-tts", Input: []string{"text"}, Output: []string{"audio"}}, want: "tts"},
		{name: "image", item: upstreamCatalogModel{Name: "demo-image", Input: []string{"text"}, Output: []string{"image"}}, want: "text2image"},
		{name: "edit", item: upstreamCatalogModel{Name: "demo-edit", Input: []string{"text", "image"}, Output: []string{"image"}}, want: "image_editing", vision: true},
		{name: "video", item: upstreamCatalogModel{Name: "demo-video", Input: []string{"text", "image"}, Output: []string{"video"}}, want: "text2video", vision: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferUpstreamCatalogModel(tc.item)
			if got.Type != tc.want || got.Vision != tc.vision {
				t.Fatalf("type=%s vision=%v, want type=%s vision=%v", got.Type, got.Vision, tc.want, tc.vision)
			}
		})
	}
	vision := inferUpstreamCatalogModel(upstreamCatalogModel{Name: "demo-vision", Input: []string{"text", "image"}, Output: []string{"text"}, Context: 1000000})
	if vision.MaxInputTokensOverride != "1000000" {
		t.Fatalf("context override = %q", vision.MaxInputTokensOverride)
	}
}

func TestUpstreamModelUsesTypedMetadataAndExactInputLimit(t *testing.T) {
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		json     string
		typeName string
		tokens   string
	}{
		{name: "typed image", json: `{"name":"opaque-model","model_type":"text2image","input":["text","image"],"output":["text","image"]}`, typeName: "text2image"},
		{name: "typed embed", json: `{"name":"opaque-model","model_type":"multimodal_embedding","input":["text","image"],"output":["text"]}`, typeName: "multimodal_embedding"},
		{name: "explicit input", json: `{"name":"gpt-4o","input":["text"],"output":["text"],"context":200000,"max_input":123456}`, typeName: "llm", tokens: "123456"},
		{name: "context beats legacy window", json: `{"name":"gpt-4o","input":["text"],"output":["text"],"context":1000000}`, typeName: "llm", tokens: "1000000"},
		{name: "binary window exact", json: `{"name":"unknown-chat","input":["text"],"output":["text"],"context":131072}`, typeName: "llm", tokens: "131072"},
		{name: "embedding input", json: `{"name":"opaque-model","model_type":"embed","input":["text"],"output":["text"],"context":8192,"max_input":8191}`, typeName: "embed", tokens: "8191"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var item upstreamCatalogModel
			if err := json.Unmarshal([]byte(tc.json), &item); err != nil {
				t.Fatal(err)
			}
			got := inferUpstreamCatalogModel(item)
			if got.Type != tc.typeName || got.MaxInputTokensOverride != tc.tokens {
				t.Fatalf("type=%q tokens=%q, want type=%q tokens=%q", got.Type, got.MaxInputTokensOverride, tc.typeName, tc.tokens)
			}
		})
	}
}

func TestBundledUpstreamCatalogHasModalities(t *testing.T) {
	path := "../config/upstream_model_catalog.json"
	if _, err := os.Stat(path); err != nil {
		t.Skip("upstream catalog is fetched at image build time and is not checked in")
	}
	catalog, err := loadUpstreamCatalog("../config/upstream_model_catalog.json")
	if err != nil {
		t.Fatalf("load upstream catalog: %v", err)
	}
	var found *upstreamCatalogModel
	for i := range catalog.Providers["Claude"] {
		model := &catalog.Providers["Claude"][i]
		if model.Name != "" && containsModality(model.Input, "image") && containsModality(model.Input, "text") && containsModality(model.Output, "text") {
			found = model
			break
		}
	}
	if found == nil {
		t.Fatal("Claude upstream catalog has no text/image chat model")
	}
	if !containsModality(found.Input, "image") || !containsModality(found.Input, "text") || !containsModality(found.Output, "text") {
		t.Fatalf("unexpected modalities: %+v", found)
	}
	if len(catalog.Providers["OpenRouter"]) == 0 {
		t.Fatal("OpenRouter catalog is empty")
	}
}

func containsModality(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSeedUpstreamModelCatalogSkipsHardcodedModels(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "catalog.yaml")
	upstreamPath := filepath.Join(dir, "upstream.json")
	yamlText := []byte(`model_providers:
  capabilities: [has_models]
  suppliers:
    - name: OpenAI
      description:
        zh-CN: 中文描述
        en-US: English description
      base_url: https://api.openai.com/v1/
      models:
        - {name: old-hardcoded, type: llm, vision: false}
        - {name: gpt-kept, type: tts, vision: false, free_auto_select_priority: 3}
    - name: MinerU
      description:
        zh-CN: 中文描述
        en-US: English description
      base_url: https://mineru.example/
      models:
        - {name: mineru-ocr, type: llm}
`)
	if err := os.WriteFile(yamlPath, yamlText, 0o644); err != nil {
		t.Fatal(err)
	}
	upstreamText := []byte(`{
      "providers": {
        "OpenAI": [
          {"name": "gpt-kept", "input": ["text", "image"], "output": ["text"], "context": 128000, "max_output": 4096},
          {"name": "gpt-vision", "model_type": "vlm", "input": ["text", "image"], "output": ["text"], "context": 200000, "max_output": 8192}
        ]
      }
    }`)
	if err := os.WriteFile(upstreamPath, upstreamText, 0o644); err != nil {
		t.Fatal(err)
	}

	db := upstreamCatalogTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.DefaultModelProvider{ID: "openai", Name: "OpenAI", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.DefaultModel{
		ID: "stale", DefaultModelProviderID: "openai", ProviderName: "OpenAI", Name: "old-hardcoded", ModelType: "llm", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err != nil {
		t.Fatal(err)
	}

	var provider orm.DefaultModelProvider
	if err := db.Where("name = ?", "OpenAI").Take(&provider).Error; err != nil {
		t.Fatal(err)
	}
	var models []orm.DefaultModel
	if err := db.Where("default_model_provider_id = ?", provider.ID).Order("name").Find(&models).Error; err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Name != "gpt-kept" || models[1].Name != "gpt-vision" {
		t.Fatalf("openai models = %+v", models)
	}
	if !models[0].Vision || models[0].FreeAutoSelectPriority != 3 || models[0].ModelType != "llm" {
		t.Fatalf("expected upstream type and YAML free policy: %+v", models[0])
	}
	if models[1].ModelType != "vlm" || !models[1].Vision || models[1].MaxInputTokens == nil || *models[1].MaxInputTokens != "200000" {
		t.Fatalf("inferred vision model = %+v", models[1])
	}

	var mineru []orm.DefaultModel
	if err := db.Where("provider_name = ?", "MinerU").Find(&mineru).Error; err != nil {
		t.Fatal(err)
	}
	if len(mineru) != 0 {
		t.Fatalf("uncovered model provider must not seed YAML models: %+v", mineru)
	}
}

const upstreamTestYAML = `model_providers:
  capabilities: [multi_group, custom_base_url, has_models]
  suppliers:
    - name: OpenAI
      description: {zh-CN: 中文描述, en-US: English description}
      base_url: https://api.openai.com/v1/
      models:
        - {name: yaml-only, type: llm}
    - name: Missing
      description: {zh-CN: 中文描述, en-US: English description}
      base_url: https://missing.example/v1/
      models:
        - {name: yaml-missing, type: llm}
    - name: Empty
      description: {zh-CN: 中文描述, en-US: English description}
      base_url: https://empty.example/v1/
      models:
        - {name: yaml-empty, type: llm}
ocr_providers:
  suppliers:
    - name: MinerU
      description: {zh-CN: 中文描述, en-US: English description}
      base_url: https://mineru.example/
translation_providers:
  suppliers:
    - name: Translation
      description: {zh-CN: 中文描述, en-US: English description}
      base_url: https://translation.example/
search_providers:
  suppliers:
    - name: Search
      description: {zh-CN: 中文描述, en-US: English description}
      base_url: https://search.example/
`

func upstreamCatalogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.DefaultModelProvider{}, &orm.DefaultModel{}, &orm.UserModelProvider{},
		&orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{},
	).DB
}

func upstreamCatalogTestPaths(t *testing.T, yamlText, upstreamText string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	yamlPath, upstreamPath := filepath.Join(dir, "catalog.yaml"), filepath.Join(dir, "upstream.json")
	for path, value := range map[string]string{yamlPath: yamlText, upstreamPath: upstreamText} {
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return yamlPath, upstreamPath
}

func TestSeedUpstreamMissingAndEmptyProvidersNeverFallBackToYAML(t *testing.T) {
	db := upstreamCatalogTestDB(t)
	yamlPath, upstreamPath := upstreamCatalogTestPaths(t, upstreamTestYAML, `{"providers":{"OpenAI":[{"name":"upstream-chat","input":["text"],"output":["text"],"context":8192}],"Empty":[]}}`)
	for _, name := range []string{"Missing", "Empty"} {
		if err := db.Create(&orm.DefaultModelProvider{ID: name, Name: name}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.DefaultModel{ID: name, DefaultModelProviderID: name, ProviderName: name, Name: "existing-" + name, ModelType: "llm"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err != nil {
			t.Fatal(err)
		}
	}
	var modelNames []string
	if err := db.Model(&orm.DefaultModel{}).Order("name").Pluck("name", &modelNames).Error; err != nil {
		t.Fatal(err)
	}
	if want := []string{"existing-Empty", "existing-Missing", "upstream-chat"}; !reflect.DeepEqual(modelNames, want) {
		t.Fatalf("default model names=%v, want %v", modelNames, want)
	}
	var providers []orm.DefaultModelProvider
	if err := db.Where("name IN ?", []string{"MinerU", "Translation", "Search"}).Order("category").Find(&providers).Error; err != nil {
		t.Fatal(err)
	}
	var categories []string
	for _, provider := range providers {
		categories = append(categories, provider.Category)
	}
	if !reflect.DeepEqual(categories, []string{"ocr", "search", "translation"}) {
		t.Fatalf("service provider categories=%v", categories)
	}
}

func TestSeedUpstreamPreservesUserModelsAndSelectionsAcrossRestart(t *testing.T) {
	db := upstreamCatalogTestDB(t)
	yamlPath, upstreamPath := upstreamCatalogTestPaths(t, upstreamTestYAML, `{"providers":{"OpenAI":[{"name":"new-chat","input":["text","image"],"output":["text"],"max_input":123456,"context":200000},{"name":"new-auto","input":["text"],"output":["text"],"context":8192}]}}`)
	create := func(value any) {
		t.Helper()
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	create(&orm.DefaultModelProvider{ID: "provider", Name: "OpenAI"})
	create(&orm.DefaultModel{ID: "old-default", DefaultModelProviderID: "provider", ProviderName: "OpenAI", Name: "old-chat", ModelType: "llm"})
	create(&orm.UserModelProvider{ID: "user-provider", DefaultModelProviderID: "provider", Name: "OpenAI"})
	create(&[]orm.UserModelProviderGroup{
		{ID: "official", UserModelProviderID: "user-provider", BaseURL: "https://api.openai.com/v1/"},
		{ID: "proxy", UserModelProviderID: "user-provider", BaseURL: "https://proxy.example/v1/"},
	})
	customLimit := "777"
	create(&[]orm.UserModelProviderGroupModel{
		{ID: "selected-old", UserModelProviderID: "user-provider", UserModelProviderGroupID: "official", Name: "old-chat", ModelType: "llm", IsDefault: true},
		{ID: "unselected-old", UserModelProviderID: "user-provider", UserModelProviderGroupID: "official", Name: "old-unused", ModelType: "llm", IsDefault: true},
		{ID: "custom", UserModelProviderID: "user-provider", UserModelProviderGroupID: "official", Name: "new-chat", ModelType: "llm", MaxInputTokens: &customLimit, IsDefault: false},
		{ID: "proxy-old", UserModelProviderID: "user-provider", UserModelProviderGroupID: "proxy", Name: "old-chat", ModelType: "llm", IsDefault: true},
	})
	create(&orm.UserSelectedModel{UserID: "user", ModelKey: "llm", UserModelProviderGroupModelID: "selected-old"})
	var insertedID string
	for i := 0; i < 2; i++ {
		if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err != nil {
			t.Fatal(err)
		}
		var added orm.UserModelProviderGroupModel
		if err := db.Where("user_model_provider_group_id = ? AND name = ?", "official", "new-auto").Take(&added).Error; err != nil {
			t.Fatal(err)
		}
		if !added.IsDefault || added.MaxInputTokens == nil || *added.MaxInputTokens != "8192" {
			t.Fatalf("upstream model was not synced: %+v", added)
		}
		if i > 0 && added.ID != insertedID {
			t.Fatalf("restart replaced user model: %s -> %s", insertedID, added.ID)
		}
		insertedID = added.ID
	}
	var modelNames []string
	if err := db.Model(&orm.DefaultModel{}).Order("name").Pluck("name", &modelNames).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(modelNames, []string{"new-auto", "new-chat"}) {
		t.Fatalf("catalog templates=%v", modelNames)
	}
	var groupModels []orm.UserModelProviderGroupModel
	if err := db.Where("id <> ?", insertedID).Order("id").Find(&groupModels).Error; err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, model := range groupModels {
		ids = append(ids, model.ID)
	}
	if !reflect.DeepEqual(ids, []string{"custom", "proxy-old", "selected-old", "unselected-old"}) {
		t.Fatalf("user models=%v", ids)
	}
	if groupModels[0].ModelType != "llm" || groupModels[0].MaxInputTokens == nil || *groupModels[0].MaxInputTokens != "777" {
		t.Fatalf("custom model overwritten: %+v", groupModels[0])
	}
	var selections []orm.UserSelectedModel
	if err := db.Find(&selections).Error; err != nil {
		t.Fatal(err)
	}
	if len(selections) != 1 || selections[0].UserModelProviderGroupModelID != "selected-old" {
		t.Fatalf("selections=%+v", selections)
	}
}

func TestSeedUpstreamMissingLimitUsesLegacyFallback(t *testing.T) {
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}
	db := upstreamCatalogTestDB(t)
	yamlPath, upstreamPath := upstreamCatalogTestPaths(t, upstreamTestYAML, `{"providers":{"OpenAI":[{"name":"gpt-4o","input":["text"],"output":["text"]},{"name":"unknown-chat","input":["text"],"output":["text"]}]}}`)
	if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err != nil {
		t.Fatal(err)
	}
	var models []orm.DefaultModel
	if err := db.Find(&models).Error; err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models=%+v", models)
	}
	for _, model := range models {
		if model.MaxInputTokens == nil || *model.MaxInputTokens != "128K" {
			t.Fatalf("fallback missing: %+v", model)
		}
	}
}

func TestSeedUpstreamRollsBackProvidersOnInvalidCatalog(t *testing.T) {
	for _, catalog := range []string{
		`{"providers":{"Unknown":[{"name":"new-chat","input":["text"],"output":["text"]}]}}`,
		`{"providers":{"OpenAI":[{"name":"","input":["text"],"output":["text"]}]}}`,
	} {
		t.Run(catalog, func(t *testing.T) {
			db := upstreamCatalogTestDB(t)
			if err := db.Create(&orm.DefaultModelProvider{ID: "existing", Name: "OpenAI", Description: "original"}).Error; err != nil {
				t.Fatal(err)
			}
			yamlPath, upstreamPath := upstreamCatalogTestPaths(t, upstreamTestYAML, catalog)
			if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err == nil {
				t.Fatal("expected invalid catalog error")
			}
			var providers, models int64
			if err := db.Model(&orm.DefaultModelProvider{}).Count(&providers).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.DefaultModel{}).Count(&models).Error; err != nil {
				t.Fatal(err)
			}
			if providers != 1 || models != 0 {
				t.Fatalf("failed catalog persisted providers=%d models=%d", providers, models)
			}
			var provider orm.DefaultModelProvider
			if err := db.Take(&provider, "id = ?", "existing").Error; err != nil {
				t.Fatal(err)
			}
			if provider.Description != "original" {
				t.Fatalf("failed catalog changed existing provider: %+v", provider)
			}
		})
	}
}
