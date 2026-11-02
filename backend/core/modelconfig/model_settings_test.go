package modelconfig

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"strings"
	"testing"
)

func TestBuildLLMConfigCarriesEffectiveFeaturesAndPrivateRequestOptions(t *testing.T) {
	rows := []SelectedRuntimeModel{{ModelType: "llm", TechnicalModelType: "vlm", ProviderName: "OpenAI", ModelName: "test", Vision: true,
		ModelSettings:   `{"feature_support":{"vision":true,"tool_calling":true,"reasoning":true},"feature_enabled":{"vision":false,"tool_calling":false}}`,
		RequestAdvanced: credentialvault.ProviderRequestAdvanced{Headers: map[string]string{"X-Gateway-Key": "private"}, Query: map[string]string{"tenant": "private-tenant"}, ExtraBody: map[string]any{"temperature": 0.1}}}}
	cfg := BuildLLMConfig(rows)
	raw, _ := json.Marshal(cfg)
	var decoded map[string]map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	role := decoded["llm"]
	f := role["features"].(map[string]any)
	if role["vision"] != false || f["vision"] != false || f["tool_calling"] != false || f["reasoning"] != true {
		t.Fatalf("features=%v", role)
	}
	advanced := role["request_advanced"].(map[string]any)
	if advanced["headers"].(map[string]any)["X-Gateway-Key"] != "private" {
		t.Fatalf("advanced not forwarded")
	}
	summary := SummarizeLLMConfigForLog(cfg)
	for _, value := range []string{"private", "private-tenant"} {
		if strings.Contains(summary, value) {
			t.Fatal("private advanced value logged")
		}
	}
}

func TestBuildLLMConfigPreservesUnknownFeaturesAndEmbeddingPrefixes(t *testing.T) {
	cfg := BuildLLMConfig([]SelectedRuntimeModel{{ModelType: "embed_main", TechnicalModelType: "embed", ProviderName: "OpenAI", ModelSettings: `{"embedding":{"query_prefix":"query: ","passage_prefix":" passage:  ","batch_size":3}}`}, {ModelType: "llm", TechnicalModelType: "llm", ProviderName: "OpenAI"}})
	raw, _ := json.Marshal(cfg)
	var decoded map[string]map[string]any
	_ = json.Unmarshal(raw, &decoded)
	e := decoded["embed_main"]["embedding"].(map[string]any)
	if e["query_prefix"] != "query: " || e["passage_prefix"] != " passage:  " || e["batch_size"] != float64(3) {
		t.Fatalf("embedding=%v", e)
	}
	f := decoded["llm"]["features"].(map[string]any)
	if _, exists := f["tool_calling"]; exists {
		t.Fatal("unknown tool support became a boolean")
	}
	if _, exists := f["reasoning"]; exists {
		t.Fatal("unknown reasoning support became a boolean")
	}
}

func TestDisabledToolsExcludeEvolutionButKeepNormalChat(t *testing.T) {
	row := SelectedRuntimeModel{ModelType: "evo_llm", TechnicalModelType: "llm", ProviderName: "OpenAI", ModelName: "test", BaseURL: "https://example.invalid/v1", ModelSettings: `{"feature_enabled":{"tool_calling":false}}`}
	if cfg := BuildLLMConfig([]SelectedRuntimeModel{row}); cfg != nil {
		t.Fatalf("evo accepted disabled tools: %v", cfg)
	}
	row.ModelType = "llm"
	if BuildLLMConfig([]SelectedRuntimeModel{row})["llm"] == nil {
		t.Fatal("plain chat was blocked")
	}
}

func TestSavedModelSettingsReachSelectedAndAdminEmbeddingRuntime(t *testing.T) {
	db, calls := unverifiedSupplierFixture(t)
	settings := `{"feature_support":{"tool_calling":true},"feature_enabled":{"tool_calling":false}}`
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "chat").Update("model_settings", settings).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "chat"}).Error; err != nil {
		t.Fatal(err)
	}
	config, err := LoadLLMConfig(context.Background(), db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(config["llm"])
	if !strings.Contains(string(raw), `"tool_calling":false`) {
		t.Fatalf("saved settings lost: %s", raw)
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "connection").Update("is_verified", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "embed").Updates(map[string]any{"model_settings": `{"embedding":{"query_prefix":"Q: ","passage_prefix":"P: ","batch_size":3}}`, "max_input_tokens": "2048"}).Error; err != nil {
		t.Fatal(err)
	}
	embed, err := LoadAdminEmbedConfig(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(embed)
	for _, expected := range []string{`"query_prefix":"Q: "`, `"passage_prefix":"P: "`, `"batch_size":3`, `"max_input_tokens":"2048"`} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("admin embed options lost: %s", raw)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("loading settings made an unexpected model request")
	}
}

func TestLegacyDedicatedVisionBindingPreservedButExplicitDisableWins(t *testing.T) {
	row := SelectedRuntimeModel{ModelType: "vlm", TechnicalModelType: "llm", ProviderName: "OpenAI"}
	if BuildLLMConfig([]SelectedRuntimeModel{row})["vlm"].(map[string]any)["vision"] != true {
		t.Fatal("legacy dedicated vision binding was dropped")
	}
	row.ModelSettings = `{"feature_enabled":{"vision":false}}`
	if BuildLLMConfig([]SelectedRuntimeModel{row}) != nil {
		t.Fatal("explicit vision disable ignored")
	}
}
