import catalog from "./upstreamModelCatalog.json";

export const modelModalities = ["text", "image", "audio", "video", "file"] as const;

export type ModelModality = (typeof modelModalities)[number];

export interface UpstreamModelRecord {
  name: string;
  input: ModelModality[];
  output: ModelModality[];
  context: number;
  max_output: number;
}

interface UpstreamCatalogFile {
  generated_at: string;
  providers: Record<string, UpstreamModelRecord[]>;
}

const bundled = catalog as UpstreamCatalogFile;

function normalizeKey(value: string) {
  return value.trim().toLowerCase();
}

const modelsByProvider = new Map<string, Map<string, UpstreamModelRecord>>();
const modelsByName = new Map<string, UpstreamModelRecord[]>();

for (const [providerName, models] of Object.entries(bundled.providers)) {
  const providerKey = normalizeKey(providerName);
  const byName = new Map<string, UpstreamModelRecord>();
  for (const model of models) {
    byName.set(normalizeKey(model.name), model);
    const bucket = modelsByName.get(normalizeKey(model.name)) || [];
    bucket.push(model);
    modelsByName.set(normalizeKey(model.name), bucket);
  }
  modelsByProvider.set(providerKey, byName);
}

export function lookupUpstreamModel(providerName: string, modelName: string) {
  const providerModels = modelsByProvider.get(normalizeKey(providerName));
  const scoped = providerModels?.get(normalizeKey(modelName));
  if (scoped) {
    return scoped;
  }
  const matches = modelsByName.get(normalizeKey(modelName)) || [];
  return matches.length === 1 ? matches[0] : undefined;
}

export function upstreamCatalogGeneratedAt() {
  return bundled.generated_at;
}
