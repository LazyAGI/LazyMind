import { TaskServiceApi, type StartTaskResult } from "./request";

export const DOC_SUMMARY_GROUP = "doc-summary";

export async function startDocSummaryParse(
  dataset: string,
  documentIds: string[],
  displayName: string,
) {
  const ids = documentIds.filter(Boolean);
  const createRes = await TaskServiceApi().createTasks(dataset, {
    parent: `datasets/${dataset}`,
    items: [
      {
        upload_file_id: "",
        task: {
          task_type: "TASK_TYPE_REPARSE",
          document_ids: ids,
          display_name: displayName,
          reparse_groups: [DOC_SUMMARY_GROUP],
        },
      },
    ],
  });
  const taskIds = (createRes.data.tasks || [])
    .map((task: { task_id?: string }) => task.task_id)
    .filter((taskId: string | undefined): taskId is string => !!taskId);
  if (!taskIds.length) {
    throw new Error("no task created");
  }
  console.info("[llmParse] created tasks", { dataset, taskIds, documentIds: ids });
  const startRes = await TaskServiceApi().startTasks(dataset, { task_ids: taskIds });
  const startedCount = startRes.data.started_count ?? 0;
  const failedTasks = ((startRes.data.tasks || []) as StartTaskResult[]).filter(
    (task: StartTaskResult) => task.status !== "STARTED",
  );
  console.info("[llmParse] start tasks response", {
    dataset,
    taskIds,
    startedCount,
    failedCount: startRes.data.failed_count,
    failedTasks,
  });
  if (startedCount <= 0) {
    const message =
      failedTasks.find((task: StartTaskResult) => task.message)?.message ||
      "no tasks submitted successfully";
    throw new Error(message);
  }
}
