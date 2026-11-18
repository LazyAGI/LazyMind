import type { GitHubStarOpportunity } from './api';
import { CHAT_HOME_PATH, getChatConversationPath } from '@/modules/chat/constants/chat';

export function isStarOpportunity(value: unknown): value is GitHubStarOpportunity {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<GitHubStarOpportunity>;
  return typeof item.task_id === 'string' && item.task_id.length > 0 && item.task_id.length <= 64
    && typeof item.conversation_id === 'string' && item.conversation_id.length > 0 && item.conversation_id.length <= 64
    && item.status === 'succeeded' && item.result_ready === true && item.source === 'final_result';
}

export function starReturnPath(value: unknown): string | null {
  if (typeof value !== 'string' || value.includes('\\')) return null;
  if (value === '/task-center' || value.startsWith('/task-center?')) return value;
  const prefix = `${CHAT_HOME_PATH}/`;
  return value.startsWith(prefix) && /^[A-Za-z0-9_-]{1,64}(?:[?#].*)?$/.test(value.slice(prefix.length)) ? value : null;
}

export function starReturnIntent(value: unknown, userId: string) {
  if (!value || typeof value !== 'object') return null;
  const item = value as { userId?: string; returnTo?: unknown; opportunity?: unknown };
  const returnTo = starReturnPath(item.returnTo);
  if (item.userId !== userId || !returnTo || !isStarOpportunity(item.opportunity)) return null;
  if (returnTo.startsWith(`${CHAT_HOME_PATH}/`)
    && returnTo.split(/[?#]/)[0] !== getChatConversationPath(item.opportunity.conversation_id)) return null;
  return { userId, returnTo, opportunity: item.opportunity };
}
