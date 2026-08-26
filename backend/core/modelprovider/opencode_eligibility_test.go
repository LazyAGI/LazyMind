package modelprovider

import "testing"

func TestResolveOpenCodeModel(t *testing.T) {
	tests := []struct {
		name, provider, model, baseURL, technicalType string
		wantOK                                        bool
		wantModel, wantBaseURL                        string
	}{
		{"qwen llm", "Qwen", "qwen-plus", "https://dashscope.aliyuncs.com/", "llm", true, "alibaba-cn/qwen-plus", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"openai vlm", "OpenAI", "gpt-4o-mini", "https://api.openai.com/v1/", "vlm", true, "openai/gpt-4o-mini", "https://api.openai.com/v1"},
		{"pr14 openai addition", "OpenAI", "gpt-5.5", "https://api.openai.com/v1/", "vlm", true, "openai/gpt-5.5", "https://api.openai.com/v1"},
		{"namespaced id preserved", "SiliconFlow", "Pro/moonshotai/Kimi-K2.6", "https://api.siliconflow.cn/v1/", "vlm", true, "siliconflow-cn/Pro/moonshotai/Kimi-K2.6", "https://api.siliconflow.cn/v1"},
		{"unknown namespace rejected", "SiliconFlow", "Other/Kimi-K2.6", "https://api.siliconflow.cn/v1/", "llm", false, "", ""},
		{"unknown provider rejected", "Custom", "gpt-4o-mini", "https://example.com/v1", "llm", false, "", ""},
		{"non chat model rejected", "Qwen", "qwen-plus", "https://dashscope.aliyuncs.com/", "embed", false, "", ""},
		{"empty endpoint rejected", "OpenAI", "gpt-4o-mini", "", "vlm", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveOpenCodeModel(tt.provider, tt.model, tt.baseURL, tt.technicalType)
			if ok != tt.wantOK || got.Model != tt.wantModel || got.BaseURL != tt.wantBaseURL {
				t.Fatalf("ResolveOpenCodeModel() = (%+v, %v), want model=%q baseURL=%q ok=%v", got, ok, tt.wantModel, tt.wantBaseURL, tt.wantOK)
			}
		})
	}
}
