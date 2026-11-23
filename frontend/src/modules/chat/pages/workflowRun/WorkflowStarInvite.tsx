import { useEffect, useState } from 'react';
import { AgentAppsAuth, AUTH_LOGOUT_EVENT, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import GitHubStarPrompt from '@/modules/githubStar/GitHubStarPrompt';
import { findGitHubStarOpportunity, type GitHubStarOpportunity } from '@/modules/githubStar/api';
import { isStarOpportunity } from '@/modules/githubStar/intent';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import type { WorkflowControlView } from '@/modules/chat/utils/workflowControl';

/** External agent workbenches share the invitation policy and always use immediate Star. */
export default function WorkflowStarInvite({ userId, session, control, resultsOnly }: {
  userId: string;
  session?: WorkflowSession;
  control?: WorkflowControlView;
  resultsOnly: boolean;
}) {
  const sessionId = session?.session_id;
  const eligible = Boolean(userId && !resultsOnly && session?.controller_host === 'external-agent'
    && control?.session_id === sessionId && control?.continuation === 'completed'
    && ['codex', 'workbuddy', 'deepseek-harness'].includes(control?.binding?.provider ?? ''));
  const scope = `${userId}:${sessionId}`;
  const [offer, setOffer] = useState<{ scope: string; opportunity: GitHubStarOpportunity } | null>(null);

  useEffect(() => {
    setOffer(null);
    if (!eligible || !sessionId) return;
    let active = true;
    const identity = AgentAppsAuth.getSessionIdentity();
    const reset = () => {
      if (AgentAppsAuth.getSessionIdentity() !== identity) { active = false; setOffer(null); }
    };
    const logout = () => { active = false; setOffer(null); };
    window.addEventListener(AUTH_USER_CHANGE_EVENT, reset);
    window.addEventListener(AUTH_LOGOUT_EVENT, logout);
    void findGitHubStarOpportunity('', sessionId).then(value => {
      if (active && AgentAppsAuth.getSessionIdentity() === identity && isStarOpportunity(value)) setOffer({ scope, opportunity: value });
    }).catch(() => {});
    return () => {
      active = false;
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, reset);
      window.removeEventListener(AUTH_LOGOUT_EVENT, logout);
    };
  }, [sessionId, eligible, scope]);

  return eligible && offer?.scope === scope ? <GitHubStarPrompt key={scope} userId={userId}
    blocked={false} returnTo='/task-center' mode='plugin' opportunity={offer.opportunity} /> : null;
}
