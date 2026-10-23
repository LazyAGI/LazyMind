export const modelFeatures = ["vision", "tool_calling", "reasoning"] as const;
export type ModelFeature = typeof modelFeatures[number];
export type ModelFeatureFlags = Partial<Record<ModelFeature, boolean>>;

export interface ModelSettings {
  feature_support?: ModelFeatureFlags;
  feature_enabled?: ModelFeatureFlags;
  embedding?: {
    query_prefix?: string;
    passage_prefix?: string;
    batch_size?: number | null;
  };
}

/** Unknown support preserves the legacy execution behavior. */
export function isModelFeatureEnabled(settings: ModelSettings | undefined, feature: ModelFeature, legacyEnabled = true): boolean {
  return settings?.feature_support?.[feature] !== false &&
    (settings?.feature_enabled?.[feature] ?? settings?.feature_support?.[feature] ?? legacyEnabled);
}

export function declaredFeatureFlags(flags?: ModelFeatureFlags): ModelFeatureFlags | undefined {
  const entries = modelFeatures.flatMap((feature) => typeof flags?.[feature] === "boolean" ? [[feature, flags[feature]]] : []);
  return entries.length ? Object.fromEntries(entries) : undefined;
}

export interface EmbeddingEditorValues {
  embeddingQueryPrefix?: string;
  embeddingPassagePrefix?: string;
  embeddingBatchSize?: number | null;
}

export function editableModelSettings(capability: string | undefined, settings?: ModelSettings, embeddingValues: EmbeddingEditorValues = {}, preserveEmpty = false): ModelSettings | undefined {
  const result: ModelSettings = {};
  if (["LLM_CHAT", "VLM", "LLM_SELF_EVOLUTION"].includes(capability || "")) {
    const support = declaredFeatureFlags(settings?.feature_support);
    const enabled = declaredFeatureFlags(settings?.feature_enabled);
    if (support) result.feature_support = support;
    if (enabled) result.feature_enabled = enabled;
  }
  if (capability === "EMBEDDING") {
    const embedding = { ...settings?.embedding };
    if (embeddingValues.embeddingQueryPrefix !== undefined) embedding.query_prefix = embeddingValues.embeddingQueryPrefix;
    if (embeddingValues.embeddingPassagePrefix !== undefined) embedding.passage_prefix = embeddingValues.embeddingPassagePrefix;
    if (embeddingValues.embeddingBatchSize !== undefined) embedding.batch_size = embeddingValues.embeddingBatchSize;
    else if (settings?.embedding && "batch_size" in settings.embedding) embedding.batch_size = null;
    if (Object.keys(embedding).length) result.embedding = embedding;
  }
  return Object.keys(result).length || preserveEmpty ? result : undefined;
}
