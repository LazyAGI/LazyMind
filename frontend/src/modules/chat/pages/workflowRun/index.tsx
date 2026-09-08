import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Spin } from 'antd';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { WorkflowPanel } from '@/modules/chat/components/WorkflowPanel';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { workflowRunControl } from '@/runtime/desktopBridge';
import { loadWorkflowRun, watchWorkflowRun } from './loadRun';

/** A standalone run page, optionally stripped to its panel for an external host surface. */
export default function WorkflowRunPage({ embedded = false }: { embedded?: boolean }) {
  const { sessionId = '' } = useParams();
  const { t } = useTranslation();
  const key = `workflow-run:${sessionId}`;
  const [error, setError] = useState('');
  const [loaded, setLoaded] = useState(false);
  const setSession = useWorkflowStore((s) => s.setSession);
  const refresh = useCallback(async () => {
    const run = await loadWorkflowRun(sessionId, WorkflowSessionApi());
    setSession(key, run);
  }, [sessionId, key, setSession]);

  useEffect(() => {
    let active = true;
    let unsubscribe: (() => void) | undefined;
    setLoaded(false);
    setError('');
    void refresh().then(() => {
      if (!active) return;
      unsubscribe = watchWorkflowRun(sessionId, () => {
        void refresh().catch((reason: unknown) => {
          if (active) setError(reason instanceof Error ? reason.message : t('chat.workflowRunLoadFailed'));
        });
      });
      setLoaded(true);
    }).catch((reason: unknown) => {
      if (active) setError(reason instanceof Error ? reason.message : t('chat.workflowRunLoadFailed'));
    });
    return () => { active = false; unsubscribe?.(); };
  }, [sessionId, refresh, t]);

  // Same as LazyMind chat: continue/retry/rollback send a chat message to the
  // host session; stop cancels the current Agent. This page's host is the DSH
  // session that started the run (via Assistant Bridge session/prompt|cancel).
  const control = async (action: 'prompt' | 'cancel', message?: string) => {
    setError('');
    try {
      await workflowRunControl(sessionId, action, message);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('chat.workflowRunControlFailed'));
    }
  };

  return (
    <main style={embedded ? { height: '100%', overflow: 'auto', padding: 12 } : { maxWidth: 1200, margin: '24px auto', padding: 24 }}>
      {!embedded && <h1>{t('chat.workflowPanelTitle')}</h1>}
      {error && <Alert type='error' showIcon message={error} />}
      {!loaded && !error && <Spin />}
      {loaded && <>
        {!embedded && <Button onClick={() => { void refresh().catch((reason: Error) => setError(reason.message)); }}>{t('chat.workflowRunRefresh')}</Button>}
        <WorkflowPanel conversationId={key} onRefresh={() => refresh().catch((reason: Error) => setError(reason.message))}
          onSendMessage={(message) => control('prompt', message)} onStop={() => { void control('cancel'); }} />
      </>}
    </main>
  );
}
