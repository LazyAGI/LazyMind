package modelprovider

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
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
		{name: "vision", item: upstreamCatalogModel{Name: "demo-vision", Input: []string{"text", "image"}, Output: []string{"text"}, Context: 1000000}, want: "vlm", vision: true},
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
	if vision.MaxInputTokensOverride != "1M" {
		t.Fatalf("context override = %q", vision.MaxInputTokensOverride)
	}
}

func TestBundledUpstreamCatalogHasModalities(t *testing.T) {
	catalog, err := loadUpstreamCatalog("../config/upstream_model_catalog.json")
	if err != nil {
		t.Fatalf("load upstream catalog: %v", err)
	}
	var found *upstreamCatalogModel
	for i := range catalog.Providers["Claude"] {
		if catalog.Providers["Claude"][i].Name == "claude-opus-4-7" {
			found = &catalog.Providers["Claude"][i]
			break
		}
	}
	if found == nil {
		t.Fatal("claude-opus-4-7 missing from Claude upstream catalog")
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
        - {name: gpt-kept, type: llm, vision: false, free_auto_select_priority: 3}
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
          {"name": "gpt-vision", "input": ["text", "image"], "output": ["text"], "context": 200000, "max_output": 8192}
        ]
      }
    }`)
	if err := os.WriteFile(upstreamPath, upstreamText, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := gorm.Open(sqlite.Open("file:"+filepath.Join(dir, "catalog.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&orm.DefaultModelProvider{},
		&orm.DefaultModel{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserModelProviderGroupModel{},
		&orm.UserSelectedModel{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.DefaultModel{
		ID: "stale", DefaultModelProviderID: "pending", ProviderName: "OpenAI", Name: "old-hardcoded", ModelType: "llm", CreatedAt: now, UpdatedAt: now,
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
		t.Fatalf("yaml metadata was not kept: %+v", models[0])
	}
	if models[1].ModelType != "vlm" || !models[1].Vision || models[1].MaxInputTokens == nil || *models[1].MaxInputTokens != "200K" {
		t.Fatalf("inferred vision model = %+v", models[1])
	}

	var mineru []orm.DefaultModel
	if err := db.Where("provider_name = ?", "MinerU").Find(&mineru).Error; err != nil {
		t.Fatal(err)
	}
	if len(mineru) != 1 || mineru[0].Name != "mineru-ocr" {
		t.Fatalf("uncovered provider should keep the YAML models: %+v", mineru)
	}
}
