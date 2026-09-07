import type { KnowledgeMarketTaskListItemOpenAPIResponse } from "@/api/generated/core-client";

export function getLatestKnowledgeMarketTasks(
  tasks: KnowledgeMarketTaskListItemOpenAPIResponse[],
) {
  const seenItems = new Set<string>();
  return [...tasks]
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
    .filter((task) => {
      // Historical jobs share the item's current parsing state. Only its latest
      // job represents that work; otherwise an update revives old install jobs.
      if (!task.market_item_id) return true;
      if (seenItems.has(task.market_item_id)) return false;
      seenItems.add(task.market_item_id);
      return true;
    });
}

export interface KnowledgeMarketTaskState {
  jobType: string;
  jobStatus: string;
  stage?: string;
  overallPercent?: number;
  progress?: {
    current: number;
    total: number;
  };
}

export function getKnowledgeMarketTaskPercent(task: KnowledgeMarketTaskState) {
  if (typeof task.overallPercent === "number") return task.overallPercent;
  if (!task.progress?.total) return 0;
  return Math.round((task.progress.current / task.progress.total) * 100);
}

export function isKnowledgeMarketTaskFailed(task: KnowledgeMarketTaskState) {
  return (
    ["failed", "canceled"].includes(task.jobStatus) || task.stage === "failed"
  );
}

export function isKnowledgeMarketTaskPartiallyFailed(
  task: KnowledgeMarketTaskState,
) {
  return task.stage === "partial_failed";
}

export function isKnowledgeMarketTaskTerminal(task: KnowledgeMarketTaskState) {
  if (
    isKnowledgeMarketTaskFailed(task) ||
    isKnowledgeMarketTaskPartiallyFailed(task)
  ) {
    return true;
  }
  if (["updateAll", "knowledge_market_update_all"].includes(task.jobType)) {
    return task.jobStatus === "succeeded";
  }
  return (
    task.stage === "done" && getKnowledgeMarketTaskPercent(task) >= 100
  );
}

export function isKnowledgeMarketTaskCompleted(task: KnowledgeMarketTaskState) {
  return (
    isKnowledgeMarketTaskTerminal(task) &&
    !isKnowledgeMarketTaskFailed(task) &&
    !isKnowledgeMarketTaskPartiallyFailed(task)
  );
}
