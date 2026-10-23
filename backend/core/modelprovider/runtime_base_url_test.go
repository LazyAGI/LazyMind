package modelprovider

import "testing"

func TestLazyLLMBaseURLNormalizesOpenAIAndOfficialOpenRouter(t *testing.T) {
	tests := []struct {
		name, provider, input, want string
	}{
		{"token plan legacy chat URL", "SenseNova", "https://token.sensenova.cn/v1/chat/completions/", "https://token.sensenova.cn/v1/chat/completions/"},
		{"token plan root", "SenseNova", "https://token.sensenova.cn/v1", "https://token.sensenova.cn/v1"},
		{"classic SenseNova", "SenseNova", "https://api.sensenova.cn/compatible-mode/v1/", "https://api.sensenova.cn/compatible-mode/v1/"},
		{"SenseNova proxy", "SenseNova", "https://proxy.example/v1/", "https://proxy.example/v1/"},
		{"host and port get v1", "OpenAI", "http://127.0.0.1:8000", "http://127.0.0.1:8000/v1/"},
		{"vllm root gets v1", "vLLM", "http://inference.test:8000", "http://inference.test:8000/v1/"},
		{"unversioned chat request uses default root", "OpenAI", "http://127.0.0.1:8000/chat/completions", "http://127.0.0.1:8000/v1/"},
		{"request suffix is cropped", "OpenAI", "http://model.test:8000/v1/chat/completions", "http://model.test:8000/v1/"},
		{"prefixed v1 is preserved", "OpenAI", "https://model.test/api/v1/models?debug=1#result", "https://model.test/api/v1/"},
		{"custom API prefix is preserved", "OpenAI", "https://model.test/random/input", "https://model.test/random/input/"},
		{"version three API is preserved", "OpenAI", "https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/v3/"},
		{"version four API is preserved", "OpenAI", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/"},
		{"coding root is preserved", "OpenAI", "https://gateway.example/api/coding", "https://gateway.example/api/coding/"},
		{"encoded proxy prefix is preserved", "OpenAI", "https://gateway.example/tenant%2Fteam/v1/", "https://gateway.example/tenant%2Fteam/v1/"},
		{"encoded version three prefix is preserved", "OpenAI", "https://gateway.example/tenant%2Fteam/v3", "https://gateway.example/tenant%2Fteam/v3/"},
		{"official endpoint stays canonical", "OpenAI", "https://api.openai.com/v1/", "https://api.openai.com/v1/"},
		{"official openrouter suffix is cropped", "OpenRouter", "https://openrouter.ai/api/v1/invalid_suffix", "https://openrouter.ai/api/v1/"},
		{"official openrouter query is cropped", "OpenRouter", "https://openrouter.ai/api/v1/?debug=1#result", "https://openrouter.ai/api/v1/"},
		{"openrouter proxy path is preserved", "OpenRouter", "https://proxy.example.com/openrouter/v1/", "https://proxy.example.com/openrouter/v1/"},
		{"other provider is untouched", "Qwen", "https://model.test/random/input?debug=1", "https://model.test/random/input?debug=1"},
		{"invalid input is left for validation", "OpenAI", "not a url", "not a url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LazyLLMBaseURL(tc.provider, tc.input); got != tc.want {
				t.Fatalf("LazyLLMBaseURL(%q, %q) = %q, want %q", tc.provider, tc.input, got, tc.want)
			}
		})
	}
}

func TestHasOpenAIRequestPath(t *testing.T) {
	tests := []struct {
		name, provider, baseURL string
		want                    bool
	}{
		{"official chat completions endpoint", "OpenAI", "https://api.openai.com/v1/chat/completions", true},
		{"vllm requires API root", "vLLM", "http://inference.test:8000/v1/chat/completions", true},
		{"proxy responses endpoint", "OpenAI", "https://proxy.example.com/openai/v1/responses", true},
		{"prefixed API root", "OpenAI", "https://proxy.example.com/openai/v1/", false},
		{"host without version", "OpenAI", "http://127.0.0.1:8000", false},
		{"other provider", "SenseNova", "https://token.sensenova.cn/v1/chat/completions/", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasOpenAIRequestPath(tc.provider, tc.baseURL); got != tc.want {
				t.Fatalf("hasOpenAIRequestPath(%q, %q) = %v, want %v", tc.provider, tc.baseURL, got, tc.want)
			}
		})
	}
}
