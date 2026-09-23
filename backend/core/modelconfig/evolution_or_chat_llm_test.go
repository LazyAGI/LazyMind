package modelconfig

import "testing"

func TestApplyEvolutionOrFallbackLLMPrefersEvo(t *testing.T) {
	configs := ApplyEvolutionOrFallbackLLM(map[string]any{
		"llm":     map[string]any{"source": "openai", "model": "chat-model"},
		"evo_llm": map[string]any{"source": "openai", "model": "evo-model", "base_url": "http://evo"},
	}, map[string]any{"source": "openai", "model": "chat-fallback"})
	llm, _ := configs["llm"].(map[string]any)
	if llm["model"] != "evo-model" {
		t.Fatalf("llm = %#v, want evo-model", llm)
	}
	evo, _ := configs["evo_llm"].(map[string]any)
	if evo["model"] != "evo-model" {
		t.Fatalf("evo_llm should remain, got %#v", configs["evo_llm"])
	}
}

func TestApplyEvolutionOrFallbackLLMUsesChatWhenEvoMissing(t *testing.T) {
	configs := ApplyEvolutionOrFallbackLLM(map[string]any{
		"embed_main": map[string]any{"source": "openai", "model": "embed"},
	}, map[string]any{"source": "openai", "model": "chat-fallback"})
	llm, _ := configs["llm"].(map[string]any)
	if llm["model"] != "chat-fallback" {
		t.Fatalf("llm = %#v, want chat-fallback", llm)
	}
}

func TestApplyEvolutionOrFallbackLLMKeepsExistingLLMWithoutEvo(t *testing.T) {
	configs := ApplyEvolutionOrFallbackLLM(map[string]any{
		"llm": map[string]any{"source": "openai", "model": "selected-llm"},
	}, map[string]any{"source": "openai", "model": "chat-fallback"})
	llm, _ := configs["llm"].(map[string]any)
	if llm["model"] != "selected-llm" {
		t.Fatalf("llm = %#v, want selected-llm", llm)
	}
}
