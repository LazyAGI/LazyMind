import { getLocalizedErrorMessage } from "@/components/request";
import { modelProbeCodes, probeFailureCode, type ModelProbeCode } from "./modelProbe";

type Translate = (key: string) => string;
type ConnectionReason = ModelProbeCode | "model_list_unavailable";

const asRecord = (value: unknown): Record<string, unknown> => value && typeof value === "object" ? value as Record<string, unknown> : {};
const isReason = (value: unknown): value is ConnectionReason => typeof value === "string" && (value === "model_list_unavailable" || (modelProbeCodes as readonly string[]).includes(value));

/** Checks and catalog discovery use the same safe reasons as per-model probes. */
export function getProviderConnectionFailureMessage(error: unknown, t: Translate, fallback?: string): string {
  const failure = asRecord(error);
  const body = asRecord(asRecord(failure.response).data);
  const detail = asRecord(body.data);
  const nested = asRecord(detail.data);
  const reason = [failure.reason, failure.code, detail.reason, detail.code, nested.reason, nested.code].find(isReason);
  if (reason === "model_list_unavailable") return t("modelProvider.connection.reason.model_list_unavailable");
  if (reason) return t(`modelProvider.probe.reason.${reason}`);
  if (failure.response || failure.code === "ECONNABORTED" || failure.code === "ETIMEDOUT")
    return t(`modelProvider.probe.reason.${probeFailureCode(error)}`);
  return fallback || getLocalizedErrorMessage(error);
}
