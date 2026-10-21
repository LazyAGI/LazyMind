export interface ImportTaskState {
  task_id?: string;
  target_pid?: string;
  display_name?: string;
  task_state?: string;
  status?: string;
  err_msg?: string;
}

export interface ImportTaskFailure {
  taskId: string;
  message: string;
  needsFFmpeg: boolean;
}

export function mergeTrackedImportTasks(previous: ImportTaskState[], created: ImportTaskState[]): ImportTaskState[] {
  const tracked = new Map<string, ImportTaskState>();
  for (const task of [...previous, ...created]) {
    if (task.task_id) tracked.set(task.task_id, { ...tracked.get(task.task_id), ...task });
  }
  return [...tracked.values()];
}

export async function inspectFinishedImportTasks(
  tasks: ImportTaskState[],
  loadTask: (id: string) => Promise<ImportTaskState>,
): Promise<ImportTaskFailure[]> {
  const failures: ImportTaskFailure[] = [];
  for (const task of tasks) {
    const taskId = String(task.task_id || "");
    if (!taskId) continue;
    const name = task.display_name || taskId;
    try {
      const finished = await loadTask(taskId);
      const error = String(finished.err_msg || "").trim();
      if (finished.task_state?.toUpperCase() !== "FAILED" && finished.status?.toUpperCase() !== "FAILED" && !error) continue;
      failures.push({
        taskId,
        message: `${finished.display_name || name}：${getImportErrorMessage(error, "任务处理失败，服务未返回具体原因")}`,
        needsFFmpeg: isFFmpegDependencyError(error),
      });
    } catch (error) {
      failures.push({ taskId, message: `${name}：无法确认导入结果：${getImportErrorMessage(error, "任务查询失败，请重试")}`, needsFFmpeg: false });
    }
  }
  return failures;
}
import { getImportErrorMessage } from "@/modules/knowledge/utils/importError";
import { isFFmpegDependencyError } from "@/modules/knowledge/utils/taskError";

