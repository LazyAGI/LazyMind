package modelprovider

import "strings"

func normalizedDeclaredModelType(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case "chat", "llm_chat", "evo_llm":
		return "llm"
	case "embedding", "embeddings", "embed_main":
		return "embed"
	case "cross_modal_embed", "embed_image":
		return "multimodal_embedding"
	case "reranking", "reranker":
		return "rerank"
	case "asr", "transcription", "speech_to_text":
		return "stt"
	case "speech", "text_to_speech":
		return "tts"
	case "image", "text_to_image", "image_generator":
		return "text2image"
	case "video", "text_to_video", "video_generator":
		return "text2video"
	case "image-editing", "image_editor":
		return "image_editing"
	}
	return value
}

// Unknown or mixed outputs remain unclassified. Input image support alone is
// not evidence of a distinct image-editing runtime task.
func classifyUpstreamModel(declared string, inputValues, outputValues []string) string {
	declared = normalizedDeclaredModelType(declared)
	input, output := modalitySet(inputValues), modalitySet(outputValues)
	if (declared == "llm" || declared == "vlm") && len(output) > 0 && !(len(output) == 1 && output["text"]) {
		declared = ""
	}
	if declared == "embed" {
		for value := range input {
			if value != "text" {
				return "multimodal_embedding"
			}
		}
	}
	switch declared {
	case "llm", "vlm", "embed", "multimodal_embedding", "rerank", "stt", "tts", "text2image", "text2video", "image_editing":
		return declared
	case "decisions", "decision", "realtime", "moderation":
		return ""
	}
	for value := range output {
		switch value {
		case "text", "image", "video", "audio", "speech", "transcription", "embeddings", "embedding", "rerank", "reranking":
		default:
			return ""
		}
	}
	switch {
	case output["embeddings"] || output["embedding"]:
		for modality := range input {
			if modality != "text" {
				return "multimodal_embedding"
			}
		}
		return "embed"
	case output["rerank"] || output["reranking"]:
		return "rerank"
	case output["transcription"]:
		return "stt"
	case output["speech"]:
		return "tts"
	case output["video"]:
		return "text2video"
	case output["image"]:
		return "text2image"
	case output["audio"]:
		if len(output) == 1 {
			return "tts"
		}
		return ""
	case len(output) == 1 && output["text"]:
		if input["audio"] && !input["text"] {
			return "stt"
		}
		return "llm"
	}
	return ""
}

// These are executable LazyLLM adapters, not the types advertised by an upstream
// model catalog. Keep this boundary aligned with modelCapabilities.ts and the
// algorithm runtime_support registry contract test. OpenAI-compatible image
// embeddings retain the existing self-hosted embedding fallback.
var runtimeModelTypesBySource = map[string][]string{
	"aiping":      {"llm", "vlm", "embed", "rerank", "text2image"},
	"claude":      {"llm", "vlm"},
	"deepseek":    {"llm", "vlm"},
	"doubao":      {"llm", "vlm", "embed", "multimodal_embedding", "text2image", "image_editing", "text2video"},
	"glm":         {"llm", "vlm", "embed", "rerank", "text2image", "stt"},
	"kimi":        {"llm", "vlm"},
	"minimax":     {"llm", "vlm", "text2image", "tts"},
	"openai":      {"llm", "vlm", "embed", "multimodal_embedding", "rerank"},
	"openrouter":  {"llm", "vlm", "embed", "text2image", "text2video", "tts", "stt"},
	"ppio":        {"llm", "vlm"},
	"qwen":        {"llm", "vlm", "embed", "multimodal_embedding", "rerank", "text2image", "image_editing", "text2video", "tts", "stt"},
	"sensenova":   {"llm", "vlm", "embed", "text2image", "image_editing"},
	"siliconflow": {"llm", "vlm", "embed", "multimodal_embedding", "rerank", "text2image", "image_editing", "text2video", "tts", "stt"},
}

// RuntimeSupportsModelType rejects combinations without an executable adapter.
// A working compatible endpoint or public catalog entry cannot add an adapter.
func RuntimeSupportsModelType(providerName, modelType string) bool {
	modelType = normalizedDeclaredModelType(modelType)
	for _, supported := range runtimeModelTypesBySource[LazyLLMSource(providerName)] {
		if modelType == supported {
			return true
		}
	}
	return false
}
