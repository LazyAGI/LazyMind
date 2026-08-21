import type { Task } from './api';

export function isTaskFinishedWithinDays(
  task: Pick<Task, 'finished_at' | 'updated_at'>,
  days: number,
  now = Date.now(),
) {
  if (!Number.isFinite(days) || days <= 0) return false;
  const finishedAt = Date.parse(task.finished_at || task.updated_at);
  if (!Number.isFinite(finishedAt)) return false;
  const cutoff = now - days * 24 * 60 * 60 * 1000;
  return finishedAt >= cutoff && finishedAt <= now;
}
