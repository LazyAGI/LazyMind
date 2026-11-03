package modelconfig

import (
	"encoding/json"
	"testing"
)

func TestBuildLLMConfigKeepsContextAndIndependentTokenLimits(t *testing.T) {
	var selected SelectedRuntimeModel
	if err := json.Unmarshal([]byte(`{"ModelType":"llm","ProviderName":"OpenAI","ModelName":"private-model","MaxInputTokens":"1M","InputTokenLimit":"128K","MaxOutputTokens":"32K"}`), &selected); err != nil {
		t.Fatal(err)
	}
	config := BuildLLMConfig([]SelectedRuntimeModel{selected})["llm"].(map[string]any)
	for key, want := range map[string]string{"max_input_tokens": "1M", "input_token_limit": "128K", "max_output_tokens": "32K"} {
		if config[key] != want {
			t.Fatalf("%s = %#v, want %q", key, config[key], want)
		}
	}
	config = BuildLLMConfig([]SelectedRuntimeModel{{ModelType: "llm", ProviderName: "OpenAI"}})["llm"].(map[string]any)
	if _, exists := config["input_token_limit"]; exists {
		t.Fatal("unset input limit was synthesized")
	}
	if _, exists := config["max_output_tokens"]; exists {
		t.Fatal("unset output limit was synthesized")
	}
}
