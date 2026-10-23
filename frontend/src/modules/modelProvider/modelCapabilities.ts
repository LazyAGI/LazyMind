import type { ModelCapability } from "./pages/ModelProvidersPage";

const runtimeModelTypes = new Set([
  "llm", "vlm", "embed", "embedding", "embed_main", "multimodal_embedding",
  "cross_modal_embed", "embed_image", "rerank", "stt", "asr", "tts",
  "text2image", "text_to_image", "text2video", "text_to_video", "image_editing",
]);

/** Normalize explicitly declared runtime aliases; never infer a type from modalities. */
export function normalizeRuntimeModelType(value: string): string {
  const normalized = value.trim().toLowerCase();
  const aliases: Record<string, string> = {
    embedding: "embed", embed_main: "embed", asr: "stt",
    text_to_image: "text2image", text_to_video: "text2video",
    cross_modal_embed: "multimodal_embedding", embed_image: "multimodal_embedding", reranker: "rerank",
  };
  return Object.prototype.hasOwnProperty.call(aliases, normalized) ? aliases[normalized] : normalized;
}

export function isKnownRuntimeModelType(value: string): boolean {
  return runtimeModelTypes.has(normalizeRuntimeModelType(value));
}

// Executable LazyLLM adapters, matching Core's runtimeModelTypesBySource and the
// algorithm registry contract. Catalog membership alone does not imply support.
const runtimeModelTypesBySource: Record<string, readonly string[]> = {
  aiping: ["llm", "vlm", "embed", "rerank", "text2image"],
  claude: ["llm", "vlm"],
  deepseek: ["llm", "vlm"],
  doubao: ["llm", "vlm", "embed", "multimodal_embedding", "text2image", "image_editing", "text2video"],
  glm: ["llm", "vlm", "embed", "rerank", "text2image", "stt"],
  kimi: ["llm", "vlm"],
  minimax: ["llm", "vlm", "text2image", "tts"],
  openai: ["llm", "vlm", "embed", "multimodal_embedding", "rerank"],
  openrouter: ["llm", "vlm", "embed", "text2image", "text2video", "tts", "stt"],
  ppio: ["llm", "vlm"],
  qwen: ["llm", "vlm", "embed", "multimodal_embedding", "rerank", "text2image", "image_editing", "text2video", "tts", "stt"],
  sensenova: ["llm", "vlm", "embed", "text2image", "image_editing"],
  siliconflow: ["llm", "vlm", "embed", "multimodal_embedding", "rerank", "text2image", "image_editing", "text2video", "tts", "stt"],
};

function runtimeSource(protocol?: string) {
  const source = (protocol || "").toLowerCase().replace(/[^a-z0-9]/g, "");
  const aliases: Record<string, string> = {
    anthropic: "claude", bigmodel: "glm", moonshot: "kimi", tongyiqianwen: "qwen",
    volcanoark: "doubao", volcengine: "doubao", zhipu: "glm", zhipuai: "glm",
    vllm: "openai", lmdeploy: "openai",
  };
  return Object.prototype.hasOwnProperty.call(aliases, source) ? aliases[source] : source;
}

const capabilityRuntimeTypes: Record<ModelCapability, string> = {
  LLM_CHAT: "llm", LLM_SELF_EVOLUTION: "llm", VLM: "vlm", EMBEDDING: "embed",
  MULTIMODAL_EMBEDDING: "multimodal_embedding", RERANK: "rerank", ASR: "stt", TTS: "tts",
  TEXT_TO_IMAGE: "text2image", TEXT_TO_VIDEO: "text2video", IMAGE_EDITING: "image_editing", UNKNOWN: "",
};

export function getRuntimeModelTypeForCapability(capability: ModelCapability): string {
  return capabilityRuntimeTypes[capability];
}

/** A protocol determines which real runtime adapters can serve a model. */
export function isModelTypeSupportedByProtocol(modelType: string, protocol?: string) {
  const normalized = normalizeRuntimeModelType(modelType);
  const source = runtimeSource(protocol);
  return runtimeModelTypes.has(normalized) && Object.prototype.hasOwnProperty.call(runtimeModelTypesBySource, source) &&
    runtimeModelTypesBySource[source].includes(normalized);
}

export function getSelectableCapabilities(
  capabilities: readonly ModelCapability[],
  protocol?: string,
): ModelCapability[] {
  const distinct = [...new Set(capabilities.map((capability) =>
    capability === "LLM_SELF_EVOLUTION" ? "LLM_CHAT" : capability,
  ))];
  return distinct.filter((capability) => isModelTypeSupportedByProtocol(capabilityRuntimeTypes[capability], protocol));
}

export function supportsModelContext(capability?: string) {
  return capability === "LLM_CHAT" || capability === "VLM" || capability === "llm" || capability === "vlm";
}

/** Discovered context may be retained before a model has an executable type. */
export function supportsStoredModelContext(capability?: string) {
  return capability === "UNKNOWN" || capability === "" || supportsModelContext(capability);
}
