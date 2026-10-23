import type { RemoteGroupModel } from "./api";
import { normalizeRuntimeModelType } from "./modelCapabilities";
import { lookupUpstreamModel } from "./upstreamCatalog";

/** Fill ID-only discovery responses without overriding explicit upstream metadata. */
export function resolveRemoteModelMetadata(protocolName: string, model: RemoteGroupModel) {
  const upstream = lookupUpstreamModel(protocolName, model.name);
  const declaredType = normalizeRuntimeModelType(model.model_type || "");
  const hasRawOutput = model.output?.some((value) => value.trim().length > 0) === true;
  const modelType = declaredType || (hasRawOutput ? "" : normalizeRuntimeModelType(upstream?.model_type || ""));
  return {
    modelType,
    vision: modelType === "llm" && (model.input ?? upstream?.input ?? []).some((value) => value === "image" || value === "video"),
  };
}
