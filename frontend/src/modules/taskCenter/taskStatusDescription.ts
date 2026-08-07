import type { TFunction } from 'i18next';
import type { StepInfo, Task } from './api';

const FAILED_STATUSES = new Set(['failed', 'interrupted']);
const SUCCEEDED_STATUSES = new Set(['completed', 'succeeded']);
const MAX_REASON_LENGTH = 120;

export function taskStatusDescription(task: Task, t: TFunction) {
  const status = task.status.toLowerCase();
  const subject = statusSubject(task, status, t);

  if (status === 'failed') {
    return t('taskCenter.taskStateFailed', {
      task: subject,
      reason: failureReason(task, t),
    });
  }
  if (status === 'canceled') {
    return t('taskCenter.taskStateCanceled', { task: subject });
  }
  if (status === 'interrupted') {
    return t('taskCenter.taskStateInterrupted', { task: subject });
  }
  if (SUCCEEDED_STATUSES.has(status)) {
    return t('taskCenter.taskStateSucceeded', { task: subject });
  }
  if (status === 'running') {
    return t('taskCenter.taskStateRunning', { task: subject });
  }
  if (status === 'waiting_inputs') {
    return naturalizeRatio(
      cleanText(task.waiting_reason) || t('taskCenter.taskStateWaitingInputs', { task: subject }),
      t,
    );
  }
  if (status === 'waiting') {
    return naturalizeRatio(
      cleanText(task.waiting_reason) || t('taskCenter.taskStateWaiting', { task: subject }),
      t,
    );
  }
  if (status === 'pending') {
    return t('taskCenter.taskStatePending', { task: subject });
  }
  return t('taskCenter.taskStateUnknown', {
    task: subject,
    status: t(`taskCenter.status${capitalize(status)}`, { defaultValue: status }),
  });
}

function statusSubject(task: Task, status: string, t: TFunction) {
  if (status === 'failed' || status === 'interrupted') {
    const failedStep = findLastStep(task.steps, (step) => FAILED_STATUSES.has(step.status.toLowerCase()));
    const label = stepLabel(failedStep);
    if (label) return label;
  }

  if (status === 'running') {
    const runningStep = findLastStep(task.steps, (step) => step.status.toLowerCase() === 'running');
    const label = stepLabel(runningStep);
    if (label) return label;
  }

  return cleanTaskName(task.schedule_name)
    || cleanTaskName(task.conversation_title)
    || cleanTaskName(task.title)
    || taskTypeFallback(task.task_type, t);
}

function failureReason(task: Task, t: TFunction) {
  const failedStep = findLastStep(task.steps, (step) => FAILED_STATUSES.has(step.status.toLowerCase()));
  const progress = isRecord(task.progress) ? task.progress : undefined;
  const reason = cleanText(failedStep?.summary)
    || cleanText(task.waiting_reason)
    || cleanText(progress?.failure_reason)
    || cleanText(progress?.error_message);
  return truncate(naturalizeRatio(reason || t('taskCenter.failureReasonUnavailable'), t));
}

function stepLabel(step?: StepInfo) {
  return cleanText(step?.current_phase)
    || cleanText(step?.title)
    || cleanText(step?.step_id);
}

function findLastStep(steps: StepInfo[] | undefined, predicate: (step: StepInfo) => boolean) {
  return [...(steps || [])].reverse().find(predicate);
}

function taskTypeFallback(taskType: string, t: TFunction) {
  const key = taskType === 'plugin_run'
    ? 'taskCenter.taskFallbackPlugin'
    : taskType === 'background_chat'
      ? 'taskCenter.taskFallbackBackground'
      : taskType === 'scheduled'
        ? 'taskCenter.taskFallbackScheduled'
        : 'taskCenter.taskFallbackGeneric';
  return t(key);
}

function naturalizeRatio(value: string, t: TFunction) {
  return value.replace(/(\d+)\s*\/\s*(\d+)/g, (_, done: string, total: string) => (
    t('taskCenter.progressItemsReady', { done, total })
  ));
}

function cleanText(value: unknown) {
  return typeof value === 'string' ? value.replace(/\s+/g, ' ').trim() : '';
}

function cleanTaskName(value: unknown) {
  return cleanText(value).replace(/^Scheduled:\s*/i, '');
}

function truncate(value: string) {
  const characters = Array.from(value);
  return characters.length > MAX_REASON_LENGTH
    ? `${characters.slice(0, MAX_REASON_LENGTH).join('')}…`
    : value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function capitalize(value: string) {
  return value ? `${value.charAt(0).toUpperCase()}${value.slice(1)}` : value;
}
