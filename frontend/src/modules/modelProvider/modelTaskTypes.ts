/** Display tasks are independent of input/output modalities and runtime roles. */
export type ModelTaskType =
  | "chat"
  | "embedding"
  | "rerank"
  | "asr"
  | "tts"
  | "image-generation"
  | "image-edit"
  | "video-generation"
  | "realtime"
  | "document"
  | "moderation"
  | "unknown";

/** Planned adapters have assets and names, but are not selectable integrations. */
export const availableModelTaskTypes: readonly ModelTaskType[] = [
  "chat",
  "embedding",
  "rerank",
  "asr",
  "tts",
  "image-generation",
  "image-edit",
  "video-generation",
];

const taskByDeclaredType: Record<string, ModelTaskType> = {
  llm_chat: "chat",
  llm_self_evolution: "chat",
  llm: "chat",
  vlm: "chat",
  embedding: "embedding",
  multimodal_embedding: "embedding",
  embed: "embedding",
  cross_modal_embed: "embedding",
  embed_image: "embedding",
  embed_main: "embedding",
  rerank: "rerank",
  asr: "asr",
  stt: "asr",
  tts: "tts",
  text_to_image: "image-generation",
  text2image: "image-generation",
  image_editing: "image-edit",
  text_to_video: "video-generation",
  text2video: "video-generation",
};

/** Only map declared capabilities/types. Model names and modalities are not proof. */
export function getModelTaskType(value?: string): ModelTaskType {
  const normalized = value?.trim().toLowerCase() || "";
  return Object.prototype.hasOwnProperty.call(taskByDeclaredType, normalized)
    ? taskByDeclaredType[normalized]
    : "unknown";
}

export function getModelTaskLabelKey(type: ModelTaskType): string {
  return `modelProvider.taskType.${type}`;
}

export function getModelTaskDescriptionKey(type: ModelTaskType): string {
  return `modelProvider.taskTypeDescription.${type}`;
}
