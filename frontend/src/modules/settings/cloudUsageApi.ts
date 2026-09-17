import { cloudApi, silentCloudRequest } from "@/api/cloudClient";

export type CloudUsageCapability =
  | "llm"
  | "evo_llm"
  | "vlm"
  | "embed_main"
  | "embed_image"
  | "reranker"
  | "text2image"
  | "image_editing"
  | "text2video"
  | "stt"
  | "tts";

export type CloudUsageMeterUnit = "token" | "image" | "second" | "item" | "document";

export interface CloudTokenPlanQuota {
  publicModelKey: string;
  capability: CloudUsageCapability;
  meterUnit: CloudUsageMeterUnit;
  periodicQuota: number;
}

export interface CloudTokenPlanUsage {
  publicModelKey: string;
  meterUnit: CloudUsageMeterUnit;
  periodicQuota: number;
  usedAmount: number;
  remainingAmount: number;
  missingUsageCount: number;
}

export interface CloudTokenPlan {
  status: "inactive" | "active";
  modelQuotas: CloudTokenPlanQuota[];
  usage: CloudTokenPlanUsage[];
}

const capabilities = new Set<CloudUsageCapability>([
  "llm", "evo_llm", "vlm", "embed_main", "embed_image", "reranker",
  "text2image", "image_editing", "text2video", "stt", "tts",
]);
const meterUnits = new Set<CloudUsageMeterUnit>(["token", "image", "second", "item", "document"]);
const maxAmount = Number.MAX_SAFE_INTEGER;

export async function fetchCloudTokenPlan(signal?: AbortSignal): Promise<CloudTokenPlan> {
  const response = await cloudApi.apiCoreCloudTokenPlanGet({ ...silentCloudRequest, signal });
  return readCloudTokenPlan(unwrap(response.data));
}

export function readCloudTokenPlan(payload: unknown): CloudTokenPlan {
  if (!isRecord(payload) || !hasOnlyKeys(payload, [
    "status", "model_quotas", "usage",
  ])) {
    throw new Error("Invalid Cloud usage response");
  }
  if (payload.status === "inactive") {
    if (Object.keys(payload).length !== 1) throw new Error("Invalid inactive Cloud usage response");
    return { status: "inactive", modelQuotas: [], usage: [] };
  }
  if (payload.status !== "active" || !Array.isArray(payload.model_quotas) || payload.model_quotas.length < 1 ||
    payload.model_quotas.length > 100 || !Array.isArray(payload.usage) || payload.usage.length > 100) {
    throw new Error("Invalid active Cloud usage response");
  }

  const modelQuotas = payload.model_quotas.map(readQuota);
  const quotaByModel = new Map<string, CloudTokenPlanQuota>();
  for (const quota of modelQuotas) {
    if (quotaByModel.has(quota.publicModelKey)) throw new Error("Duplicate Cloud usage quota");
    quotaByModel.set(quota.publicModelKey, quota);
  }
  const usage = payload.usage.map(readUsage);
  const usageModels = new Set<string>();
  for (const item of usage) {
    const quota = quotaByModel.get(item.publicModelKey);
    if (!quota || quota.meterUnit !== item.meterUnit || quota.periodicQuota !== item.periodicQuota || usageModels.has(item.publicModelKey)) {
      throw new Error("Invalid Cloud usage meter");
    }
    usageModels.add(item.publicModelKey);
  }

  return {
    status: "active",
    modelQuotas,
    usage,
  };
}

function readQuota(payload: unknown): CloudTokenPlanQuota {
  if (!isRecord(payload) || !hasOnlyKeys(payload, ["public_model_key", "capability", "meter_unit", "periodic_quota"]) ||
    typeof payload.public_model_key !== "string" || !isPublicModelKey(payload.public_model_key) ||
    typeof payload.capability !== "string" || !capabilities.has(payload.capability as CloudUsageCapability) ||
    typeof payload.meter_unit !== "string" || !meterUnits.has(payload.meter_unit as CloudUsageMeterUnit) ||
    !isPositiveInteger(payload.periodic_quota)) {
    throw new Error("Invalid Cloud usage quota");
  }
  return {
    publicModelKey: payload.public_model_key,
    capability: payload.capability as CloudUsageCapability,
    meterUnit: payload.meter_unit as CloudUsageMeterUnit,
    periodicQuota: payload.periodic_quota,
  };
}

function readUsage(payload: unknown): CloudTokenPlanUsage {
  if (!isRecord(payload) || !hasOnlyKeys(payload, [
    "public_model_key", "meter_unit", "periodic_quota", "used_amount", "remaining_amount", "missing_usage_count",
  ]) || typeof payload.public_model_key !== "string" || !isPublicModelKey(payload.public_model_key) ||
    typeof payload.meter_unit !== "string" || !meterUnits.has(payload.meter_unit as CloudUsageMeterUnit) ||
    !isPositiveInteger(payload.periodic_quota) || !isNonNegativeInteger(payload.used_amount) ||
    !isNonNegativeInteger(payload.remaining_amount) || !isNonNegativeInteger(payload.missing_usage_count)) {
    throw new Error("Invalid Cloud usage meter");
  }
  return {
    publicModelKey: payload.public_model_key,
    meterUnit: payload.meter_unit as CloudUsageMeterUnit,
    periodicQuota: payload.periodic_quota,
    usedAmount: payload.used_amount,
    remainingAmount: payload.remaining_amount,
    missingUsageCount: payload.missing_usage_count,
  };
}

function unwrap(payload: unknown): unknown {
  if (isRecord(payload) && "data" in payload) return payload.data;
  return payload;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasOnlyKeys(value: Record<string, unknown>, allowed: string[]) {
  const set = new Set(allowed);
  return Object.keys(value).every((key) => set.has(key));
}

function isPositiveInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) > 0 && Number(value) <= maxAmount;
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 0 && Number(value) <= maxAmount;
}

function isPublicModelKey(value: string) {
  return /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/.test(value) && value.length <= 96;
}
