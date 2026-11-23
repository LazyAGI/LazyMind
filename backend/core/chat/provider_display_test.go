package chat

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"strings"
	"testing"
)

func TestChatSupplierDisplayIdentitySurvivesRouteAndSnapshot(t *testing.T) {
	for _, name := range []string{"DoubaoCodingPlan", "DoubaoAgentPlan", "QwenCodingPlan", "QwenTokenPlan", "QwenTokenPlanTeam", "GLMCodingPlan", "SenseNovaTokenPlan", "MiniMaxMPlan", "KimiCode", "My proxy"} {
		t.Run(name, func(t *testing.T) {
			adapter := "OpenAI"
			if name == "KimiCode" {
				adapter = "Claude"
			}
			model := &availableChatModel{ID: "model", ProviderID: "supplier", ProviderName: adapter, ProviderDisplayName: name, ProviderIsCustom: name == "My proxy", ModelName: "test-model", Source: "own"}
			model.APIKey = "test-key"
			config, err := buildChatLLMConfig(t.Context(), nil, model)
			if err != nil {
				t.Fatal(err)
			}
			runtime, ok := config.(map[string]any)
			if !ok || runtime["source"] != strings.ToLower(adapter) {
				t.Fatalf("supplier display identity changed runtime adapter: %#v", config)
			}
			route := fixedChatModelRoute(model)
			snapshot := chatModelSnapshotForModel(model)
			body := map[string]any{chatModelRouteBodyKey: route}
			restored := chatModelRouteFromHistoryExt(mergeChatModelRouteIntoExt(nil, body))
			snapshotJSON, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			restoredSnapshot := chatModelRouteFromSnapshot(&orm.Conversation{ChatModelSnapshot: snapshotJSON}, "auto")
			inherited := inheritedTaskModelOrigin(body)

			for _, value := range []any{route, snapshot, restored, restoredSnapshot, inherited.Identity, (&chatFallbackState{models: map[string]availableChatModel{"own:model": *model}}).identity("own:model")} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				if got["provider_display_name"] != name {
					t.Errorf("display identity lost: %s", data)
				}
				if got["provider_name"] != adapter {
					t.Errorf("runtime adapter changed: %s", data)
				}
				if name == "My proxy" && got["provider_is_custom"] != true {
					t.Errorf("custom identity lost: %s", data)
				}
			}
		})
	}
}
