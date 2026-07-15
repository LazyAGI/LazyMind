import type { TFunction } from "i18next";
import { CLOUD_SYNC_POLL_INTERVAL_MS, CLOUD_SYNC_TIMEOUT_MS } from "../constants/options";
import {
  type ScanV2AgentHint,
  type ScanV2Binding,
  type ScanV2Client,
} from "./scanAccessors";

export function sleep(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function createCloudSyncError(code?: string) {
  const error = new Error("Cloud sync failed") as Error & {
    error_code: string;
  };
  error.error_code = `${code || "2000509"}`;
  return error;
}

export async function waitForCloudSyncRun(
  client: ScanV2Client,
  sourceId: string,
  _t: TFunction,
  runIds: string[] = [],
) {
  const deadline = Date.now() + CLOUD_SYNC_TIMEOUT_MS;
  const expectedRuns = Math.max(runIds.length, 1);

  while (Date.now() < deadline) {
    const [detailResponse, summaryResponse] = await Promise.all([
      client
        .getSource({ sourceId }, { silentError: true } as never)
        .catch(() => null),
      client
        .getSourceSummary({ sourceId }, { silentError: true } as never)
        .catch(() => null),
    ]);
    const bindings = (detailResponse?.data.bindings || []) as ScanV2Binding[];
    const summary = summaryResponse?.data as Record<string, any> | undefined;
    const errorBinding = bindings.find((item) => {
      const status = `${item.status || ""}`.toUpperCase();
      return status.includes("FAILED") || status.includes("ERROR") || status.includes("CANCEL");
    });
    const status = `${errorBinding?.status || summary?.status || ""}`.toUpperCase();
    const summaryBindings = Array.isArray(summary?.bindings)
      ? (summary.bindings as Record<string, any>[])
      : [];
    const finishedBindings = summaryBindings.filter((item) =>
      Boolean(item.last_success_at || item.lastSuccessAt),
    );

    if (
      finishedBindings.length >= expectedRuns ||
      (expectedRuns === 1 && Boolean(summary?.last_success_at || summary?.lastSuccessAt))
    ) {
      return { run_ids: runIds, status: "SUCCEEDED" };
    }

    if (
      status.includes("FAILED") ||
      status.includes("ERROR") ||
      status.includes("CANCEL")
    ) {
      const rawError = errorBinding?.last_error || errorBinding?.lastError;
      const errorCode =
        typeof rawError === "string"
          ? rawError
          : rawError?.code || rawError?.error_code;
      throw createCloudSyncError(errorCode);
    }

    await sleep(CLOUD_SYNC_POLL_INTERVAL_MS);
  }

  throw createCloudSyncError("2000509");
}

export function pickScanAgent(agents: ScanV2AgentHint[], preferredAgentId?: string) {
  if (preferredAgentId) {
    const preferred = agents.find((item) => item.agent_id === preferredAgentId);
    if (preferred) {
      return preferred;
    }
  }

  const onlineAgent = agents.find((item) => {
    const status = (item.status || "").toLowerCase();
    return (
      status.includes("online") ||
      status.includes("active") ||
      status.includes("running")
    );
  });

  return onlineAgent || agents[0];
}
