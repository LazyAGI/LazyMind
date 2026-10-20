package modelconfig

import "testing"

func TestBuildLLMConfigSenseNovaPreservesConfiguredURL(t *testing.T) {
	for _, chatURL := range []string{"https://token.sensenova.cn/v1/", "https://token.sensenova.cn/v1/chat/completions/", "https://proxy.example/prefix/v2/"} {
		for _, role := range []string{"text2image", "image_editing", "image_generator", "image_editor", "llm", "vlm"} {
			t.Run(role, func(t *testing.T) {
				config := BuildLLMConfig([]SelectedRuntimeModel{{
					ModelType: role, ProviderName: "SenseNova", ModelName: "sensenova-u1.5-lite",
					BaseURL: chatURL, APIKey: "test-key",
				}})
				got := config[role].(map[string]any)
				want := chatURL

				if got["base_url"] != want {
					t.Fatalf("base_url = %v, want %s", got["base_url"], want)
				}
				if got["source"] != "sensenova" || got["model"] != "sensenova-u1.5-lite" || got["api_key"] != "test-key" {
					t.Fatal("model selection or credentials changed")
				}
			})
		}
	}

}

func TestBuildLLMConfigOtherImageProviderPreservesURL(t *testing.T) {
	const baseURL = "https://api.siliconflow.cn/v1/"
	config := BuildLLMConfig([]SelectedRuntimeModel{{
		ModelType: "text2image", ProviderName: "SiliconFlow", ModelName: "Kwai-Kolors/Kolors", BaseURL: baseURL,
	}})
	if got := config["text2image"].(map[string]any)["base_url"]; got != baseURL {
		t.Fatalf("base_url = %v, want %s", got, baseURL)
	}
}
