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
  };
  return Object.prototype.hasOwnProperty.call(aliases, normalized) ? aliases[normalized] : normalized;
}

function isAnthropicProtocol(protocol?: string) {
  return /^(claude|anthropic)$/i.test(protocol?.trim() || "");
}

/** A protocol determines which real runtime adapters can serve a model. */
export function isModelTypeSupportedByProtocol(modelType: string, protocol?: string) {
  const normalized = normalizeRuntimeModelType(modelType);
  return runtimeModelTypes.has(normalized) &&
    (!isAnthropicProtocol(protocol) || normalized === "llm" || normalized === "vlm");
}

export function getSelectableCapabilities(
  capabilities: readonly ModelCapability[],
  protocol?: string,
): ModelCapability[] {
  const distinct = [...new Set(capabilities.map((capability) =>
    capability === "LLM_SELF_EVOLUTION" ? "LLM_CHAT" : capability,
  ))];
  return isAnthropicProtocol(protocol)
    ? distinct.filter((capability) => capability === "LLM_CHAT" || capability === "VLM")
    : distinct;
}

export function supportsModelContext(capability?: string) {
  return capability === "LLM_CHAT" || capability === "VLM" || capability === "llm" || capability === "vlm";
}
