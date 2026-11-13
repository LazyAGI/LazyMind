import type { GitHubStarOpportunity } from './api';

export function isStarOpportunity(value: unknown): value is GitHubStarOpportunity {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<GitHubStarOpportunity>;
  return typeof item.task_id === 'string' && item.task_id.length > 0 && item.task_id.length <= 64
    && typeof item.conversation_id === 'string' && item.conversation_id.length > 0 && item.conversation_id.length <= 64
    && item.status === 'succeeded' && item.result_ready === true && item.source === 'final_result';
}

export function starReturnPath(value: unknown): string | null {
  return typeof value === 'string' && (value === '/task-center' || value.startsWith('/task-center?')) && !value.includes('\\')
    ? value : null;
}

export function starReturnIntent(value: unknown, userId: string) {
  if (!value || typeof value !== 'object') return null;
  const item = value as { userId?: string; returnTo?: unknown; opportunity?: unknown };
  const returnTo = starReturnPath(item.returnTo);
  return item.userId === userId && returnTo && isStarOpportunity(item.opportunity)
    ? { userId, returnTo, opportunity: item.opportunity } : null;
}
