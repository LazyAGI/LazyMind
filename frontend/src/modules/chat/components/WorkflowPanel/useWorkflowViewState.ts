import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import { WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX } from '@/modules/chat/constants/chat';
import type { WorkflowSession, WorkflowUI } from '@/modules/chat/store/workflowPanel';
import type { WorkflowFollowMode } from './workflowFollowMode';

interface WorkflowViewState {
  mode: WorkflowFollowMode;
  tabId: string;
  expanded: boolean;
  collapsed: boolean;
}

function readView(key: string): Partial<WorkflowViewState> {
  try {
    const value = JSON.parse(localStorage.getItem(key) ?? '{}');
    if (!value || typeof value !== 'object') return {};
    return {
      ...(value.mode === 'following' || value.mode === 'free' ? { mode: value.mode } : {}),
      ...(typeof value.tabId === 'string' ? { tabId: value.tabId } : {}),
      ...(typeof value.expanded === 'boolean' ? { expanded: value.expanded } : {}),
      ...(typeof value.collapsed === 'boolean' ? { collapsed: value.collapsed } : {}),
    };
  } catch { return {}; }
}

function persistView(key: string, value: WorkflowViewState) {
  try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* Keep this page usable without storage. */ }
}

/** Layout choices belong to a workflow run, never to the preceding run in the same chat. */
export function useWorkflowViewState(session: WorkflowSession | null | undefined, ui: WorkflowUI, ready: boolean) {
  const identity = () => AgentAppsAuth.getUserInfo()?.userId ?? 'anonymous';
  const [userId, setUserId] = useState(identity);
  useEffect(() => {
    const changed = () => setUserId(identity());
    window.addEventListener(AUTH_USER_CHANGE_EVENT, changed);
    return () => window.removeEventListener(AUTH_USER_CHANGE_EVENT, changed);
  }, []);
  const key = session ? `lazymind:workflow-view:${encodeURIComponent(userId)}:${session.session_id}` : '';
  const sessionStatus = session?.status;
  const conversationId = session?.conversation_id;
  const initial = useMemo<WorkflowViewState>(() => {
    let legacyExpanded = false;
    if (ui.source_type !== 'builtin') {
      try { legacyExpanded = localStorage.getItem(`${WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX}${conversationId}`) === 'true'; } catch { /* Optional legacy preference. */ }
    }
    const running = sessionStatus === 'active' || sessionStatus === 'waiting';
    return {
      mode: ui.follow_mode?.default === 'following' ? 'following' : 'free',
      tabId: '', collapsed: false,
      expanded: (ui.source_type === 'builtin' && running) || (ui.default_panel_mode === 'expanded' && running) || legacyExpanded,
      ...readView(key),
    };
  }, [key, conversationId, sessionStatus, ui.source_type, ui.default_panel_mode, ui.follow_mode?.default]);
  const [saved, setSaved] = useState<{ key: string; value: WorkflowViewState } | null>(null);
  const value = saved?.key === key ? saved.value : initial;
  const latest = useRef({ key, value });
  latest.current = { key, value };

  useEffect(() => {
    if (!key || !ready || saved?.key === key) return;
    setSaved({ key, value: initial });
    persistView(key, initial);
  }, [key, ready, saved?.key, initial]);

  const update = useCallback((patch: Partial<WorkflowViewState>) => {
    if (!key || latest.current.key !== key) return;
    const next = { ...latest.current.value, ...patch };
    if (Object.keys(patch).every(field => next[field as keyof WorkflowViewState] === latest.current.value[field as keyof WorkflowViewState])) return;
    latest.current = { key, value: next };
    setSaved(latest.current);
    persistView(key, next);
  }, [key]);
  return { view: value, update, key };
}
