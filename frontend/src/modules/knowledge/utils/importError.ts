import type { StartTasksResponse } from "@/api/generated/core-client";
import { localizeErrorCode } from "@/components/request";

/** Only explicit per-task failures prove that an unstarted task can be retried. */
export class ImportTaskStartError extends Error {
  constructor(message: string, readonly rejectedTaskIds: readonly string[]) {
    super(message);
    this.name = 'ImportTaskStartError';
  }
}

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/** Preserve the actual import error instead of replacing it with a generic success/failure. */
export function getImportErrorMessage(error: unknown, fallback = "导入失败，请重试"): string {
  const source = error as { response?: { data?: { message?: unknown; detail?: unknown; error?: unknown; err_msg?: unknown } }; message?: unknown; detail?: unknown } | null;
  const data = source?.response?.data;
  const message = text(data?.message) || text(data?.err_msg) || text(data?.error) || text(source?.message) || text(error);
  const detail = text(data?.detail) || text(source?.detail);
  const resolved = localizeErrorCode(message, message) || fallback;
  return detail && detail !== message ? `${resolved}（${detail}）` : resolved;
}

/** HTTP 200 may carry rejected task submissions; that is still an import failure. */
export function assertImportTasksStarted(data: StartTasksResponse | undefined, expectedCount: number): void {
  const tasks = Array.isArray(data?.tasks) ? data.tasks : [];
  const failed = tasks.filter((task) => task.status?.toUpperCase() !== "STARTED" || ["FAILED", "REJECTED"].includes(task.submit_status?.toUpperCase() || ""));
  if (failed.length) {
    throw new ImportTaskStartError(failed.map((task) => {
      const reason = getImportErrorMessage(task, "任务未能启动");
      return task.display_name ? `${task.display_name}：${reason}` : reason;
    }).join("\n"), failed.filter((task) => task.status?.toUpperCase() === "FAILED"
      || ["FAILED", "REJECTED"].includes(task.submit_status?.toUpperCase() || ""))
      .map((task) => text(task.task_id)).filter(Boolean));
  }
  if (!data || !tasks.length || data.failed_count > 0 || data.started_count !== tasks.length || tasks.length < expectedCount || data.started_count < data.requested_count) {
    throw new Error("导入任务未全部启动，服务返回了失败或不完整的结果。请在导入任务中查看详情后重试。");
  }
}
