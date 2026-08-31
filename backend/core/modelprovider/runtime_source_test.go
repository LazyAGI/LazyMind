package modelprovider

import "testing"

func TestLazyLLMSourceDependsOnProviderName(t *testing.T) {
	tests := map[string]string{
		" OpenRouter ":     "openrouter",
		"Open-Router!!":    "openrouter",
		"Silicon Flow":     "siliconflow",
		"Tongyi-Qianwen":   "qwen",
		"Anthropic":        "claude",
		"Zhipu AI":         "glm",
		"future-provider!": "futureprovider",
	}
	for input, want := range tests {
		if got := LazyLLMSource(input); got != want {
			t.Errorf("LazyLLMSource(%q) = %q, want %q", input, got, want)
		}
	}
}
