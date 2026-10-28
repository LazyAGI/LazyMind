package modelprovider

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/modelsettings"
)

func TestUpstreamFeatureSupportSeedsTemplatesAndPreservesUserDeclarations(t *testing.T) {
	db := upstreamCatalogTestDB(t)
	yamlPath, upstreamPath := upstreamCatalogTestPaths(t, upstreamTestYAML, `{"schema_version":4,"providers":{"OpenAI":[{"name":"opaque","model_type":"llm","input":["text"],"output":["text"],"context":8192,"feature_support":{"vision":false,"tool_calling":true,"reasoning":false}}]}}`)
	for _, row := range []any{
		&orm.DefaultModelProvider{ID: "provider", Name: "OpenAI"},
		&orm.UserModelProvider{ID: "user-provider", DefaultModelProviderID: "provider", Name: "OpenAI"},
		&orm.UserModelProviderGroup{ID: "group", UserModelProviderID: "user-provider"},
		&orm.UserModelProviderGroup{ID: "manual-group", UserModelProviderID: "user-provider"},
		&orm.UserModelProviderGroupModel{ID: "legacy", UserModelProviderID: "user-provider", UserModelProviderGroupID: "group", Name: "opaque", ModelType: "llm", IsDefault: true},
		&orm.UserModelProviderGroupModel{ID: "manual", UserModelProviderID: "user-provider", UserModelProviderGroupID: "manual-group", Name: "opaque", ModelType: "llm", ModelSettings: `{"feature_support":{"tool_calling":false},"feature_enabled":{"tool_calling":false,"reasoning":true}}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := SeedUpstreamModelCatalog(t.Context(), db, yamlPath, upstreamPath); err != nil {
		t.Fatal(err)
	}
	var template orm.DefaultModel
	if err := db.Where("name = ?", "opaque").Take(&template).Error; err != nil {
		t.Fatal(err)
	}
	var settings modelsettings.Settings
	if err := json.Unmarshal([]byte(template.ModelSettings), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.FeatureSupport == nil || settings.FeatureSupport.ToolCalling == nil || !*settings.FeatureSupport.ToolCalling || settings.FeatureSupport.Reasoning == nil || *settings.FeatureSupport.Reasoning || settings.FeatureEnabled != nil {
		t.Fatalf("template lost declared support or invented enabled state: %s", template.ModelSettings)
	}
	var legacy, manual orm.UserModelProviderGroupModel
	if err := db.Where("id = ?", "legacy").Take(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", "manual").Take(&manual).Error; err != nil {
		t.Fatal(err)
	}
	if legacy.ModelSettings != template.ModelSettings {
		t.Fatalf("legacy support was not inherited: %s", legacy.ModelSettings)
	}
	if manual.ModelSettings != `{"feature_support":{"tool_calling":false},"feature_enabled":{"tool_calling":false,"reasoning":true}}` {
		t.Fatalf("manual settings changed: %s", manual.ModelSettings)
	}
}

func TestUpstreamFeatureSupportRecognitionUsesIdentityAndRetainsUnknownConflicts(t *testing.T) {
	var catalog upstreamCatalogFile
	if err := json.Unmarshal([]byte(`{"providers":{"OpenAI":[{"name":"scoped","model_type":"llm","feature_support":{"tool_calling":false}}]},"recognition_models":[{"name":"scoped","model_type":"llm","feature_support":{"tool_calling":true}},{"name":"vendor-a/shared-v1","model_type":"llm","feature_support":{"tool_calling":true,"reasoning":true}},{"name":"vendor-b/shared-v1","model_type":"llm","feature_support":{"tool_calling":false,"reasoning":true}},{"name":"vendor-c/shared-v1","model_type":"llm","feature_support":{"tool_calling":true}}]}`), &catalog); err != nil {
		t.Fatal(err)
	}
	index := newUpstreamModelRecognitionIndex(catalog)
	scoped := index.featureSupport("OpenAI", "scoped")
	if scoped == nil || scoped.ToolCalling == nil || *scoped.ToolCalling || scoped.Reasoning != nil {
		t.Fatalf("scoped false or unknown lost: %+v", scoped)
	}
	shared := index.featureSupport("Custom", "gateway/shared-v1")
	if shared == nil || shared.ToolCalling != nil || shared.Reasoning == nil || !*shared.Reasoning {
		t.Fatalf("conflicting tools should remain unknown: %+v", shared)
	}
	fuzzy := index.featureSupport("Custom", "gateway/shared_v1")
	if fuzzy == nil || fuzzy.ToolCalling != nil || fuzzy.Reasoning == nil || !*fuzzy.Reasoning {
		t.Fatalf("separator aliases changed evidence: %+v", fuzzy)
	}
	if index.featureSupport("Custom", "shared-v2") != nil {
		t.Fatal("capability inferred from a different version")
	}
	*shared.Reasoning = false
	if !*index.featureSupport("Custom", "gateway/shared-v1").Reasoning {
		t.Fatal("caller mutated the published recognition index")
	}
}

func TestRemoteModelFeatureSupportPrefersLiveDeclarationsAndKeepsMissingUnknown(t *testing.T) {
	var catalog upstreamCatalogFile
	if err := json.Unmarshal([]byte(`{"providers":{"OpenRouter":[{"name":"opaque","model_type":"llm","feature_support":{"tool_calling":true,"reasoning":false}}]}}`), &catalog); err != nil {
		t.Fatal(err)
	}
	index := newUpstreamModelRecognitionIndex(catalog)
	var entry openaiModelListEntry
	if err := json.Unmarshal([]byte(`{"id":"opaque","model_type":"llm","tool_call":false,"supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text"],"output_modalities":["text"]}}`), &entry); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(remoteModelItem(entry, "OpenRouter", index))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		FeatureSupport *modelsettings.FeatureFlags `json:"feature_support"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	flags := result.FeatureSupport
	if flags == nil || flags.Vision == nil || *flags.Vision || flags.ToolCalling == nil || *flags.ToolCalling || flags.Reasoning == nil || !*flags.Reasoning {
		t.Fatalf("live feature evidence lost: %s", raw)
	}
	raw, err = json.Marshal(remoteModelItem(openaiModelListEntry{ID: "unknown", ModelType: "llm"}, "OpenRouter", index))
	if err != nil {
		t.Fatal(err)
	}
	result.FeatureSupport = nil
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.FeatureSupport != nil {
		t.Fatalf("missing evidence became a declaration: %s", raw)
	}
	for _, parameters := range []string{"null", `"tools"`, `[null]`} {
		var malformed openaiModelListEntry
		if err := json.Unmarshal([]byte(`{"id":"unknown","model_type":"llm","supported_parameters":`+parameters+`}`), &malformed); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(remoteModelItem(malformed, "OpenRouter", index))
		if err != nil {
			t.Fatal(err)
		}
		result.FeatureSupport = nil
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if result.FeatureSupport != nil {
			t.Fatalf("malformed parameter evidence became a declaration: %s", raw)
		}
	}
}
