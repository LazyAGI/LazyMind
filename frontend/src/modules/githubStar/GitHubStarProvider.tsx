import { useEffect, useRef, type ReactNode } from 'react';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import { findGitHubStarOpportunity, GITHUB_STAR_OPPORTUNITY_EVENT, listGitHubStarWorkTasks } from './api';
import { isStarOpportunity } from './intent';
import GitHubStarPrompt from './GitHubStarPrompt';

export default function GitHubStarProvider({ userId, conversationId, children }: {
  userId: string; conversationId: string; children: ReactNode;
}) {
  const entry = useConversationRunningStore(state => state.entries[conversationId]);
  const observed = useRef({ userId, conversationId, taskIds: new Set<string>() });
  useEffect(() => {
    if (observed.current.userId !== userId || observed.current.conversationId !== conversationId) {
      observed.current = { userId, conversationId, taskIds: new Set<string>() };
    }
    if (!userId || !conversationId || entry?.status !== 'running') return;
    const controller = new AbortController();
    const scope = observed.current;
    void listGitHubStarWorkTasks(conversationId, controller.signal).then(tasks => {
      if (controller.signal.aborted || observed.current !== scope) return;
      for (const task of tasks) {
        if (task.status === 'running' || task.status === 'waiting') scope.taskIds.add(task.id);
      }
    }).catch(() => {});
    return () => controller.abort();
  }, [userId, conversationId, entry?.status, entry?.confirmedAt]);
  useEffect(() => {
    if (!userId || !conversationId || entry?.status !== 'idle') return;
    const taskIds = [...observed.current.taskIds];
    observed.current.taskIds.clear();
    if (entry.terminalStatus !== 'completed' || !taskIds.length) return;
    let active = true;
    void findGitHubStarOpportunity(conversationId, undefined, taskIds).then(result => {
      if (active && isStarOpportunity(result) && result.conversation_id === conversationId) {
        window.dispatchEvent(new CustomEvent(GITHUB_STAR_OPPORTUNITY_EVENT, { detail: result }));
      }
    }).catch(() => {});
    return () => { active = false; };
  }, [userId, conversationId, entry?.status, entry?.terminalStatus, entry?.terminalVersion]);
  return <>
    {children}
    {userId && <GitHubStarPrompt userId={userId} conversationId={conversationId} blocked={Boolean(conversationId && entry?.status !== 'idle')} returnTo='/task-center' />}
  </>;
}
