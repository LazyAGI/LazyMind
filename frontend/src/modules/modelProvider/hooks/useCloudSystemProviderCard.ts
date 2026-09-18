import { useTranslation } from "react-i18next";

export interface CloudSystemProviderModel {
  id: string;
  name: string;
  modelType: string;
  availability: "available" | "degraded" | "unavailable";
  lifecycle?: "active" | "deprecated" | "retired";
}

const modelTypeLabelKeys: Record<string, string> = {
  llm: "modelProvider.capability.llmChat",
  evo_llm: "modelProvider.capability.selfEvolution",
  vlm: "modelProvider.capability.vlm",
  embed_main: "modelProvider.capability.embedding",
  embed_image: "modelProvider.capability.multimodalEmbedding",
  reranker: "modelProvider.capability.rerank",
  text2image: "modelProvider.capability.textToImage",
  image_editing: "modelProvider.capability.imageEditing",
  text2video: "modelProvider.capability.textToVideo",
  stt: "modelProvider.capability.asr",
  tts: "modelProvider.capability.tts",
};

export function useCloudSystemProviderCard(models: CloudSystemProviderModel[]) {
  const { t } = useTranslation();
  const availableCount = models.filter((model) => model.availability !== "unavailable").length;
  const displayModels = models.map((model) => ({
    ...model,
    modelTypeLabel: t(modelTypeLabelKeys[model.modelType] || model.modelType),
  }));
  return { t, availableCount, displayModels };
}
