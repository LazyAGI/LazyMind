package modelprovider

import "testing"

func TestClassifyUpstreamModelPreservesTaskMeaning(t *testing.T) {
	for _, tc := range []struct {
		name, declared string
		input, output  []string
		want           string
	}{
		{name: "chat", input: []string{"text", "image"}, output: []string{"text"}, want: "llm"},
		{name: "text embedding", input: []string{"text"}, output: []string{"embeddings"}, want: "embed"},
		{name: "stale chat declaration cannot override embedding output", declared: "llm", input: []string{"text"}, output: []string{"embeddings"}, want: "embed"},
		{name: "stale chat declaration cannot override unknown output", declared: "llm", input: []string{"text"}, output: []string{"decisions"}},
		{name: "multimodal embedding", input: []string{"text", "image"}, output: []string{"embeddings"}, want: "multimodal_embedding"},
		{name: "document embedding", input: []string{"pdf"}, output: []string{"embeddings"}, want: "multimodal_embedding"},
		{name: "rerank", input: []string{"text"}, output: []string{"rerank"}, want: "rerank"},
		{name: "image with accompanying text", input: []string{"text", "image"}, output: []string{"text", "image"}, want: "text2image"},
		{name: "explicit editor", declared: "image_editing", input: []string{"text", "image"}, output: []string{"image"}, want: "image_editing"},
		{name: "video", input: []string{"text", "image"}, output: []string{"video"}, want: "text2video"},
		{name: "speech", input: []string{"text"}, output: []string{"speech"}, want: "tts"},
		{name: "transcription", input: []string{"audio"}, output: []string{"transcription"}, want: "stt"},
		{name: "legacy transcription", input: []string{"audio"}, output: []string{"text"}, want: "stt"},
		{name: "text with speech is unresolved", input: []string{"text"}, output: []string{"text", "audio"}},
		{name: "decisions are unresolved", input: []string{"text"}, output: []string{"decisions"}},
		{name: "missing metadata is unresolved"},
		{name: "future outputs are unresolved", input: []string{"text"}, output: []string{"future"}},
		{name: "mixed future outputs are unresolved", input: []string{"text"}, output: []string{"image", "future"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyUpstreamModel(tc.declared, tc.input, tc.output); got != tc.want {
				t.Fatalf("type=%q want %q", got, tc.want)
			}
		})
	}
}

func TestOpenRouterRuntimeSupportMatchesImplementedAdapters(t *testing.T) {
	for _, typ := range []string{"llm", "vlm", "embed", "text2image", "text2video", "tts", "stt"} {
		if !RuntimeSupportsModelType("OpenRouter", typ) {
			t.Errorf("implemented adapter %s rejected", typ)
		}
	}
	for _, typ := range []string{"rerank", "image_editing", "multimodal_embedding", "cross_modal_embed", "embed_image", "", "decisions"} {
		if RuntimeSupportsModelType("OpenRouter", typ) {
			t.Errorf("missing adapter %s accepted", typ)
		}
	}
	for _, provider := range []string{"OpenAI", "Qwen", "SenseNova"} {
		if !RuntimeSupportsModelType(provider, "rerank") {
			t.Errorf("changed other provider %s", provider)
		}
	}
}
