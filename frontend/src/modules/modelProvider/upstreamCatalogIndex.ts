import { normalizeRuntimeModelType } from "./modelCapabilities";

export const modelModalities = ["text", "image", "audio", "video", "file", "embeddings", "speech", "transcription", "rerank", "decisions"] as const;

export type ModelModality = string;

export interface UpstreamModelRecord {
  name: string;
  model_type?: string;
  input: ModelModality[];
  output: ModelModality[];
  context: number;
  max_output: number;
}

export interface UpstreamCatalogFile {
  schema_version?: number;
  generated_at: string;
  providers: Record<string, UpstreamModelRecord[]>;
  recognition_models?: UpstreamModelRecord[];
}

function normalizeKey(value: string) {
  return value.trim().toLowerCase();
}

export function createUpstreamCatalogIndex(catalog?: UpstreamCatalogFile) {
  const modelsByProvider = new Map<string, Map<string, UpstreamModelRecord[]>>();
  const modelsByName = new Map<string, UpstreamModelRecord[]>();
  const modelsByBasename = new Map<string, UpstreamModelRecord[]>();

  const add = (index: Map<string, UpstreamModelRecord[]>, name: string, model: UpstreamModelRecord) => {
    if (!name) return;
    const bucket = index.get(name) || [];
    bucket.push(model);
    index.set(name, bucket);
  };
  const basename = (name: string) => name.slice(name.lastIndexOf("/") + 1);
  const addGlobal = (model: UpstreamModelRecord) => {
    const name = normalizeKey(model.name);
    add(modelsByName, name, model);
    add(modelsByBasename, basename(name), model);
  };
  const unambiguous = (matches: UpstreamModelRecord[]) => {
    const typed = matches.filter((model) => model.model_type?.trim());
    const types = new Set(typed.map((model) => normalizeRuntimeModelType(model.model_type!)));
    if (types.size > 1) return undefined;
    if (typed.length) return typed[0];
    return matches.length === 1 ? matches[0] : undefined;
  };

  for (const [providerName, models] of Object.entries(catalog?.providers ?? {})) {
    const byName = new Map<string, UpstreamModelRecord[]>();
    for (const model of models) {
      const name = normalizeKey(model.name);
      add(byName, name, model);
      addGlobal(model);
    }
    modelsByProvider.set(normalizeKey(providerName), byName);
  }
  for (const model of catalog?.recognition_models ?? []) addGlobal(model);

  return {
    generatedAt: catalog?.generated_at ?? "",
    lookupModel(providerName: string, modelName: string) {
      const name = normalizeKey(modelName);
      const scoped = modelsByProvider.get(normalizeKey(providerName))?.get(name);
      if (scoped) {
        return unambiguous(scoped);
      }
      const exact = modelsByName.get(name);
      if (exact) return unambiguous(exact);
      return unambiguous(modelsByBasename.get(basename(name)) || []);
    },
  };
}
