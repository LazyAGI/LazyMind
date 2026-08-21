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

export function isKnowledgeMarketTaskTerminal(task: KnowledgeMarketTaskState) {
  if (isKnowledgeMarketTaskFailed(task)) return true;
  if (["updateAll", "knowledge_market_update_all"].includes(task.jobType)) {
    return task.jobStatus === "succeeded";
  }
  return (
    task.stage === "done" && getKnowledgeMarketTaskPercent(task) >= 100
  );
}

export function isKnowledgeMarketTaskCompleted(task: KnowledgeMarketTaskState) {
  return (
    isKnowledgeMarketTaskTerminal(task) && !isKnowledgeMarketTaskFailed(task)
  );
}
