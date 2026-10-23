export const modelModalities = ["text", "image", "audio", "video", "file"] as const;

export type ModelModality = (typeof modelModalities)[number];

export interface UpstreamModelRecord {
  name: string;
  model_type?: string;
  input: ModelModality[];
  output: ModelModality[];
  context: number;
  max_output: number;
}

export interface UpstreamCatalogFile {
  generated_at: string;
  providers: Record<string, UpstreamModelRecord[]>;
}

function normalizeKey(value: string) {
  return value.trim().toLowerCase();
}

export function createUpstreamCatalogIndex(catalog?: UpstreamCatalogFile) {
  const modelsByProvider = new Map<string, Map<string, UpstreamModelRecord>>();
  const modelsByName = new Map<string, UpstreamModelRecord[]>();

  for (const [providerName, models] of Object.entries(catalog?.providers ?? {})) {
    const byName = new Map<string, UpstreamModelRecord>();
    for (const model of models) {
      const name = normalizeKey(model.name);
      byName.set(name, model);
      const bucket = modelsByName.get(name) || [];
      bucket.push(model);
      modelsByName.set(name, bucket);
    }
    modelsByProvider.set(normalizeKey(providerName), byName);
  }

  return {
    generatedAt: catalog?.generated_at ?? "",
    lookupModel(providerName: string, modelName: string) {
      const name = normalizeKey(modelName);
      const scoped = modelsByProvider.get(normalizeKey(providerName))?.get(name);
      if (scoped) {
        return scoped;
      }
      const matches = modelsByName.get(name) || [];
      return matches.length === 1 ? matches[0] : undefined;
    },
  };
}
