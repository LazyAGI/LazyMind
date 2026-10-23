import type { RemoteGroupModel } from "./api";
import { isKnownRuntimeModelType, normalizeRuntimeModelType } from "./modelCapabilities";
import { lookupUpstreamModel } from "./upstreamCatalog";
import { declaredFeatureFlags } from "./modelSettings";

/** Legacy ID-only responses may use the catalog; Core's explicit type is authoritative. */
export function resolveRemoteModelMetadata(protocolName: string, model: RemoteGroupModel) {
  const upstream = lookupUpstreamModel(protocolName, model.name);
  const hasCoreType = Object.prototype.hasOwnProperty.call(model, "model_type");
  const declared = model.model_type?.trim() || "";
  const declaredType = isKnownRuntimeModelType(declared) ? normalizeRuntimeModelType(declared) : "";
  const hasRawOutput = model.output?.some((value) => value.trim().length > 0) === true;
  const hasOfficialTypes = model.sub_types?.some((value) => value.trim().length > 0) === true;
  const catalogType = upstream?.model_type || "";
  const fallbackType = isKnownRuntimeModelType(catalogType) ? normalizeRuntimeModelType(catalogType) : "";
  const modelType = declaredType || (!hasCoreType && !hasRawOutput && !hasOfficialTypes ? fallbackType : "");
  const visualInput = (model.input ?? (!hasCoreType ? upstream?.input : undefined) ?? []).some((value) => value === "image" || value === "video");
  const chatModel = modelType === "llm" || modelType === "vlm";
  const support = chatModel ? declaredFeatureFlags({
    ...(visualInput && (modelType === "llm" || modelType === "vlm") ? { vision: true } : {}),
    ...(!hasCoreType ? upstream?.feature_support : undefined),
    ...model.feature_support,
    ...model.model_settings?.feature_support,
  }) : undefined;
  const modelSettings = chatModel && (model.model_settings || support) ? {
    ...model.model_settings,
    ...(support ? { feature_support: support } : {}),
  } : modelType === "embed" ? model.model_settings : undefined;
  return {
    modelType,
    vision: modelType === "llm" && (support?.vision ?? visualInput),
    ...(modelSettings ? { modelSettings } : {}),
  };
}
