import type { TFunction } from "i18next";
import { CLOUD_SYNC_POLL_INTERVAL_MS, CLOUD_SYNC_TIMEOUT_MS } from "../constants/options";
import {
  getBindingLastError,
  type ScanV2AgentHint,
  type ScanV2Binding,
  type ScanV2Client,
} from "./scanAccessors";

export function sleep(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

export async function waitForCloudSyncRun(
  client: ScanV2Client,
  sourceId: string,
  t: TFunction,
  runIds: string[] = [],
) {
  const deadline = Date.now() + CLOUD_SYNC_TIMEOUT_MS;
  const expectedRuns = Math.max(runIds.length, 1);

  while (Date.now() < deadline) {
    const [detailResponse, summaryResponse] = await Promise.all([
      client.getSource({ sourceId }).catch(() => null),
      client.getSourceSummary({ sourceId }).catch(() => null),
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
      throw new Error(
        getBindingLastError(errorBinding) ||
          t("admin.dataSourceDetailCloudSyncFailedFallback"),
      );
    }

    await sleep(CLOUD_SYNC_POLL_INTERVAL_MS);
  }

  throw new Error(t("admin.dataSourceDetailCloudSyncTimeout"));
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
