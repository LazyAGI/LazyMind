import type { RemoteGroupModel } from "./api";
import { isKnownRuntimeModelType, normalizeRuntimeModelType } from "./modelCapabilities";
import { lookupUpstreamModel } from "./upstreamCatalog";

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
  return {
    modelType,
    vision: modelType === "llm" && (model.input ?? upstream?.input ?? []).some((value) => value === "image" || value === "video"),
  };
}
