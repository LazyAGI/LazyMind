import { axiosInstance } from "@/components/request";
import { AGENT_API_BASE, resultKindGateStepMap } from "./constants";
import { getNestedArrayField, getNumberField, getStringField, isRecord } from "./fields";
import type { WorkflowResultKind } from "./types";

export type ThreadGateSummary = {
  step: string;
  artifactId?: string;
  versions: number[];
  effectiveVersion?: number;
  latestVersion?: number;
};

export function normalizeThreadGateRecord(value: unknown): ThreadGateSummary | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const step = getStringField(value, ["step"]);
  if (!step) {
    return undefined;
  }
  const versions = (Array.isArray(value.versions) ? value.versions : [])
    .map((item) => (typeof item === "number" ? item : Number(item)))
    .filter((item) => Number.isFinite(item) && item > 0);
  return {
    step,
    artifactId: getStringField(value, ["artifact_id", "artifactId"]),
    versions,
    effectiveVersion: getNumberField(value, ["effective_version", "effectiveVersion"]),
    latestVersion: getNumberField(value, ["latest_version", "latestVersion"]),
  };
}

export function resolveThreadGateVersion(gate: ThreadGateSummary): number | undefined {
  if (gate.effectiveVersion && gate.effectiveVersion > 0) {
    return gate.effectiveVersion;
  }
  if (gate.latestVersion && gate.latestVersion > 0) {
    return gate.latestVersion;
  }
  const maxVersion = gate.versions.reduce((max, item) => Math.max(max, item), 0);
  return maxVersion > 0 ? maxVersion : undefined;
}

export function buildThreadGatesListUrl(threadId: string) {
  return `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/gates`;
}

export function buildThreadGateVersionUrl(threadId: string, step: string, version: number) {
  return `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/gates/${encodeURIComponent(step)}/versions/${version}`;
}

export function normalizeGateContentResponse(value: unknown): unknown {
  if (!isRecord(value)) {
    return value;
  }
  if (value.content !== undefined && value.content !== null) {
    return value.content;
  }
  return value;
}

export async function fetchThreadGateContent(
  threadId: string,
  kind: WorkflowResultKind,
  options?: { version?: number; signal?: AbortSignal },
): Promise<unknown> {
  const step = resultKindGateStepMap[kind];
  if (!step) {
    throw new Error(`unsupported result kind: ${kind}`);
  }

  let version = options?.version;
  if (!version || version <= 0) {
    const listResponse = await axiosInstance.get(buildThreadGatesListUrl(threadId), {
      signal: options?.signal,
      silentError: true,
    } as Parameters<typeof axiosInstance.get>[1]);
    const gates = getNestedArrayField(listResponse.data, ["gates"])
      .map(normalizeThreadGateRecord)
      .filter((item): item is ThreadGateSummary => Boolean(item));
    const gate = gates.find((item) => item.step === step);
    if (!gate) {
      throw { response: { status: 404 } };
    }
    version = resolveThreadGateVersion(gate);
    if (!version) {
      throw { response: { status: 404 } };
    }
  }

  const response = await axiosInstance.get(
    buildThreadGateVersionUrl(threadId, step, version),
    {
      signal: options?.signal,
      silentError: true,
    } as Parameters<typeof axiosInstance.get>[1],
  );
  return normalizeGateContentResponse(response.data);
}
