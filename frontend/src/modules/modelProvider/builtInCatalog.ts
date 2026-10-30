import type { RemoteGroupModel } from "./api";
import type { UpstreamCatalogFile } from "./upstreamCatalogIndex";

const snapshots = import.meta.glob<UpstreamCatalogFile>("./upstreamModelCatalog.json", { eager: true, import: "default" });

// Use the repository's generated upstream catalog; these are suggestions, not
// evidence of account access or a successful live /models request.
export function builtInCatalog(providerName: string): RemoteGroupModel[] {
  const providers = snapshots["./upstreamModelCatalog.json"]?.providers || {};
  const models = Object.entries(providers).find(([name]) => name.toLowerCase() === providerName.toLowerCase())?.[1] || [];
  return models.map((model) => ({
    id: model.name, name: model.name, added: false, model_type: model.model_type,
    input: model.input, output: model.output,
    max_input_tokens: model.context ? String(model.context) : undefined,
    feature_support: model.feature_support,
  }));
}
