package modelprovider

import (
	"testing"

	"lazymind/core/common/orm"
)

func TestVisionReadinessUsesSavedModelFeatureGates(t *testing.T) {
	for _, fixture := range []struct {
		name, modelType, settings string
		legacyVision, want        bool
	}{
		{"disabled", "llm", `{"feature_enabled":{"vision":false}}`, true, false},
		{"unsupported", "llm", `{"feature_support":{"vision":false}}`, true, false},
		{"explicitly enabled", "llm", `{"feature_enabled":{"vision":true}}`, false, true},
		{"declared support", "llm", `{"feature_support":{"vision":true}}`, false, true},
		{"invalid metadata", "llm", `{`, true, false},
		{"legacy VLM", " VLM ", `{}`, false, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			db, _, _, model := setupProbeTest(t, &orm.UserSelectedModel{})
			if err := db.Model(&model).Updates(map[string]any{"provider_name": "OpenAI", "model_type": fixture.modelType, "vision": fixture.legacyVision, "model_settings": fixture.settings}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.UserSelectedModel{UserID: "probe-owner", ModelKey: "llm", UserModelProviderGroupModelID: model.ID}).Error; err != nil {
				t.Fatal(err)
			}
			if ready, err := hasVisionLLMSelection(t.Context(), db, "probe-owner"); err != nil || ready != fixture.want {
				t.Fatalf("vision readiness = %v, %v; want %v", ready, err, fixture.want)
			}
			if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "probe-owner").Update("model_type", "vlm").Error; err != nil {
				t.Fatal(err)
			}
			if ready, err := hasValidModelSelection(t.Context(), db, "probe-owner", "vlm", false); err != nil || ready != fixture.want {
				t.Fatalf("dedicated vision readiness = %v, %v; want %v", ready, err, fixture.want)
			}
		})
	}
}

func TestAutomaticVisionChoiceUsesSavedModelFeatureGates(t *testing.T) {
	models := []orm.UserModelProviderGroupModel{
		{ID: "disabled", ModelType: "vlm", Vision: true, FreeAutoSelectPriority: 1, ModelSettings: `{"feature_enabled":{"vision":false}}`},
		{ID: "unsupported", ModelType: "llm", Vision: true, ModelSettings: `{"feature_support":{"vision":false}}`},
		{ID: "allowed", ModelType: "llm", ModelSettings: `{"feature_support":{"vision":true}}`},
	}
	got, ok := preferredAutoModel("https://example.test/v1/", []string{"vlm"}, models)
	if !ok || got.ID != "allowed" {
		t.Fatalf("automatic vision selection ignored feature gates: %#v, %v", got, ok)
	}
	if got, ok := preferredAutoModel("https://example.test/v1/", []string{"vlm"}, models[:2]); ok {
		t.Fatalf("automatic vision selection accepted disabled models: %#v", got)
	}
}
