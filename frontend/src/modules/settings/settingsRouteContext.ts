import type { ModelCapability } from "@/modules/modelProvider/components/DefaultModelConfigPanel";

const MODEL_CAPABILITY_TARGETS = new Set<ModelCapability>([
  "llm",
  "embed_main",
  "vlm",
  "reranker",
  "speech_to_text",
  "tts",
  "image_generator",
  "video_generator",
  "embed_image",
  "image_editor",
  "evo_llm",
]);

export function settingsModelTarget(value: string | null): ModelCapability | undefined {
  return value && MODEL_CAPABILITY_TARGETS.has(value as ModelCapability)
    ? value as ModelCapability
    : undefined;
}

export function settingsReturnTo(value: string | null): string | undefined {
  if (!value || !value.startsWith("/agent/chat/") || value.startsWith("//")) {
    return undefined;
  }
  return value;
}

export function settingsRouteParams(
  current: URLSearchParams,
  next: Record<string, string>,
): URLSearchParams {
  const params = new URLSearchParams(next);
  for (const key of ["return_to", "target", "provider_id"]) {
    const value = current.get(key);
    if (value) params.set(key, value);
  }
  return params;
}
