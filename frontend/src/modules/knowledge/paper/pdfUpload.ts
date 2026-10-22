import { TaskServiceApi } from '../utils/request';
import { buildUploadTaskItems } from '../utils/uploadByHash';
import { getImportErrorMessage, ImportTaskStartError } from '../utils/importError';
import type { TaskResponse } from '@/api/generated/core-client';

interface UploadedPaperPdf { documentId: string; tasks: TaskResponse[]; }

export function createPaperPdfUploader(datasetId: string) {
  // A File belongs to the pending confirmation. Keep its task even if starting
  // fails so retry does not bind the same bytes to a second document.
  const attempts = new WeakMap<File, { task: TaskResponse; canRestart: boolean }>();
  return async (file: File, startMode: 'DEFAULT' | 'UPLOAD'): Promise<UploadedPaperPdf> => {
    const api = TaskServiceApi();
    let attempt = attempts.get(file);
    let task = attempt?.task;
    let resume = false;
    if (task) {
      const recovered = (await api.getTask(datasetId, task.task_id!)).data;
      if (recovered.task_id !== task.task_id || (task.document_id && recovered.document_id !== task.document_id)) {
        throw new Error('无法确认原 PDF 导入任务，请在导入任务中检查后重试');
      }
      const state = recovered.task_state?.trim().toUpperCase();
      const error = recovered.err_msg?.trim();
      const succeeded = state === 'SUCCEEDED' || state === 'SUCCESS';
      if (succeeded || ['RUNNING', 'WORKING', 'STARTED', 'SUBMITTED', 'PROCESSING'].includes(state || '')) {
        if (error && !(succeeded && error.toLowerCase() === 'success')) throw new Error(getImportErrorMessage(error));
        if (!recovered.document_id?.trim()) throw new Error('原 PDF 导入任务缺少文档 ID，请在导入任务中检查');
        return { documentId: recovered.document_id, tasks: [recovered] };
      }
      resume = state === 'FAILED' || state === 'CANCELED';
      if (!resume && !['CREATING', 'UPLOADED'].includes(state || '')) {
        throw new Error(getImportErrorMessage(error, '无法确认原 PDF 导入状态，请在导入任务中检查后重试'));
      }
      if (!resume && !attempt?.canRestart) {
        throw new Error('上次 PDF 导入任务的启动结果尚未确认。请稍后重试或在导入任务中检查状态；原文档已保留。');
      }
      task = recovered;
    } else {
      const items = await buildUploadTaskItems({ datasetId, fileItems: [{ originFile: file, path: file.name }] });
      if (!items.length) throw new Error('PDF 上传未返回文件信息，请重试');
      const created = await api.createTasks(datasetId, { items });
      if (created.data.tasks?.length !== 1 || !created.data.tasks[0].task_id?.trim()) {
        throw new Error('文件已上传，但未能确认导入任务，请在导入任务中检查');
      }
      task = created.data.tasks[0];
      attempt = { task, canRestart: true };
      attempts.set(file, attempt);
    }
    // A lost response can leave the original request running. Do not submit a
    // still-creating task again unless the server explicitly rejected its start.
    attempt!.canRestart = false;
    let started;
    try {
      started = resume
        ? await api.resumeTask(datasetId, task.task_id!)
        : await api.startTasks(datasetId, { task_ids: [task.task_id!], start_mode: startMode });
    } catch (error) {
      attempt!.canRestart = error instanceof ImportTaskStartError && error.rejectedTaskIds.includes(task.task_id!);
      throw error;
    }
    const result = started.data.tasks?.find((item) => item.task_id === task.task_id);
    if (!result || (result.document_id && task.document_id && result.document_id !== task.document_id)) {
      throw new Error('PDF 导入任务返回了不匹配的结果，请在导入任务中检查');
    }
    const documentId = result.document_id || task.document_id;
    if (!documentId?.trim()) throw new Error('PDF 上传未返回有效文档 ID，请重试');
    attempt!.task = { ...task, document_id: documentId };
    return { documentId, tasks: [attempt!.task] };
  };
}
