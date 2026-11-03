package modelconfig

import "testing"

func TestOpenRouterRuntimeExportsOnlyImplementedAdapters(t *testing.T) {
	for _, tc := range []struct {
		role, technical string
		supported       bool
	}{
		{"llm", "llm", true}, {"vlm", "vlm", true}, {"embed_main", "embed", true},
		{"text2image", "text2image", true}, {"text2video", "text2video", true},
		{"tts", "tts", true}, {"stt", "stt", true},
		{"reranker", "rerank", false}, {"embed_image", "multimodal_embedding", false},
		{"text2image", "image_editing", false}, {"llm", "unknown", false},
	} {
		t.Run(tc.role+"/"+tc.technical, func(t *testing.T) {
			rows := []SelectedRuntimeModel{{ProviderName: "OpenRouter", ModelType: tc.role, TechnicalModelType: tc.technical, ModelName: "public/model"}}
			config := BuildLLMConfig(rows)
			_, exists := config[tc.role]
			if exists != tc.supported {
				t.Fatalf("runtime role %s exported=%v, want %v", tc.role, exists, tc.supported)
			}
			if got := len(eligibleRuntimeModels(rows)) > 0; got != tc.supported {
				t.Fatalf("eligible=%v, want %v", got, tc.supported)
			}
		})
	}
	if config := BuildLLMConfig([]SelectedRuntimeModel{{ProviderName: "OpenAI", ModelType: "reranker", TechnicalModelType: "rerank", ModelName: "rank"}}); config["reranker"] == nil {
		t.Fatal("other provider capability changed")
	}
}
