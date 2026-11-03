package modelprovider

import (
	"reflect"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestUpstreamClassificationRepairsOnlyCatalogModelsAndInvalidSelections(t *testing.T) {
	db := upstreamCatalogTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.DefaultModelProvider{ID: "router", Name: "OpenRouter", Category: "model"}).Error; err != nil {
		t.Fatal(err)
	}
	parent := orm.UserModelProvider{ID: "user-router", DefaultModelProviderID: "router", Name: "OpenRouter", ProtocolName: "OpenRouter", Category: "model", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{ID: "store", UserModelProviderID: parent.ID, BaseURL: "https://openrouter.ai/api/v1/", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	tokens := "4096"
	for _, entry := range []struct {
		id, name string
		catalog  bool
	}{
		{"image", "image-now", true}, {"manual", "manual-now", false},
		{"unknown", "opaque-now", true}, {"rerank", "rerank-now", true},
	} {
		row := orm.UserModelProviderGroupModel{ID: entry.id, UserModelProviderID: parent.ID, UserModelProviderGroupID: "store", ProviderName: "OpenRouter", Name: entry.name, ModelType: "llm", IsDefault: entry.catalog, InputTokenLimit: &tokens, MaxOutputTokens: &tokens, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, selection := range []orm.UserSelectedModel{
		{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "image"},
		{UserID: "owner", ModelKey: "text2image", UserModelProviderGroupModelID: "image"},
		{UserID: "manual-owner", ModelKey: "llm", UserModelProviderGroupModelID: "manual"},
		{UserID: "unknown-owner", ModelKey: "llm", UserModelProviderGroupModelID: "unknown"},
		{UserID: "rank-owner", ModelKey: "reranker", UserModelProviderGroupModelID: "rerank"},
		{UserID: "shared-owner", ModelKey: "llm", UserModelProviderGroupModelID: "image"},
	} {
		if err := db.Create(&selection).Error; err != nil {
			t.Fatal(err)
		}
	}
	yamlPath, upstreamPath := upstreamCatalogTestPaths(t, `model_providers:
  capabilities: [has_models]
  suppliers:
    - name: OpenRouter
      base_url: https://openrouter.ai/api/v1/
      description: {zh-CN: OpenRouter, en-US: OpenRouter}
`, `{"providers":{"OpenRouter":[
  {"name":"image-now","input":["text","image"],"output":["image"]},
  {"name":"manual-now","input":["text"],"output":["image"]},
  {"name":"opaque-now","input":["text"],"output":["decisions"]},
  {"name":"rerank-now","model_type":"rerank","input":["text"],"output":["rerank"]},
  {"name":"new-catalog-only","input":["text"],"output":["text"]}
]}}`)
	for i := 0; i < 2; i++ {
		if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err != nil {
			t.Fatal(err)
		}
	}
	var models []orm.UserModelProviderGroupModel
	if err := db.Order("id").Find(&models).Error; err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, model := range models {
		got[model.ID] = model.ModelType
		if model.ID != "manual" && (model.InputTokenLimit != nil || model.MaxOutputTokens != nil) {
			t.Fatalf("non-chat model kept token overrides: %+v", model)
		}
		if model.ID == "manual" && (model.InputTokenLimit == nil || *model.InputTokenLimit != tokens || model.IsDefault) {
			t.Fatalf("manual configuration changed: %+v", model)
		}
	}
	if want := map[string]string{"image": "text2image", "manual": "llm", "unknown": "unknown", "rerank": "rerank"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("models=%v, want %v", got, want)
	}
	var selections []orm.UserSelectedModel
	if err := db.Order("user_id").Find(&selections).Error; err != nil {
		t.Fatal(err)
	}
	if len(selections) != 2 || selections[0].UserID != "manual-owner" || selections[1].ModelKey != "text2image" {
		t.Fatalf("unexpected surviving defaults: %+v", selections)
	}
	var unknown orm.DefaultModel
	if err := db.Where("name = ?", "opaque-now").Take(&unknown).Error; err != nil || unknown.ModelType != "unknown" {
		t.Fatalf("unknown template=%+v err=%v", unknown, err)
	}
}

func TestSchemaTwoUnclassifiedEntryDoesNotBecomeChat(t *testing.T) {
	db := upstreamCatalogTestDB(t)
	yamlPath, upstreamPath := upstreamCatalogTestPaths(t, upstreamTestYAML, `{"schema_version":2,"providers":{"OpenAI":[{"name":"opaque-task","model_type":"","input":["text"],"output":["text"]}]}}`)
	if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err != nil {
		t.Fatal(err)
	}
	var model orm.DefaultModel
	if err := db.Where("name = ?", "opaque-task").Take(&model).Error; err != nil {
		t.Fatal(err)
	}
	if model.ModelType != "unknown" {
		t.Fatalf("unclassified task was inferred as %q", model.ModelType)
	}
}
