import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Spin } from 'antd';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AgentAppsAuth } from '@/components/auth';
import { WorkflowPanel } from '@/modules/chat/components/WorkflowPanel';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { controlActions, ReviewRefreshRequired, type WorkflowActionIntent } from '@/modules/chat/utils/workflowControl';
import { loadWorkflowRunSnapshot, watchWorkflowRun, controlStatusKey, type WorkflowRunSnapshot } from './loadRun';
import './index.scss';

/** Shared run workbench: user intent goes to Core, never to an interpreted chat prompt. */
export default function WorkflowRunPage({ embedded = false }: { embedded?: boolean }) {
  const { sessionId = '' } = useParams();
  const { t } = useTranslation();
  const user = AgentAppsAuth.getUserInfo();
  const key = `workflow-run:${window.location.origin}:${user?.tenantId ?? user?.tenant_id ?? ''}:${user?.userId ?? user?.username ?? ''}:${sessionId}`;
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [snapshot, setSnapshot] = useState<WorkflowRunSnapshot>();
  const latest = useRef<WorkflowRunSnapshot>();
  const request = useRef(0);
  const lifetime = useRef<{ key: string; controller: AbortController }>();
  const translate = useRef(t);
  translate.current = t;
  const api = useMemo(() => WorkflowSessionApi(), []);
  const setSession = useWorkflowStore(s => s.setSession);
  const refresh = useCallback(async () => {
    const current = lifetime.current;
    if (!current || current.key !== key) throw new DOMException('Run page closed', 'AbortError');
    const sequence = ++request.current;
    const value = await loadWorkflowRunSnapshot(sessionId, api, { signal: current.controller.signal });
    if (current.controller.signal.aborted || lifetime.current !== current) throw new DOMException('Run page closed', 'AbortError');
    const older = value.control && latest.current?.control && value.control.state_version < latest.current.control.state_version;
    if (!older && sequence === request.current) {
      latest.current = value;
      setSnapshot(value);
      setSession(key, value.session);
    }
    return latest.current ?? value;
  }, [sessionId, api, key, setSession]);
  const actions = useMemo(() => controlActions(async () => {
    const value = await refresh();
    if (!value.control) throw new Error('Legacy workflow controls are unavailable');
    return value.control;
  }, async command => {
    const current = lifetime.current;
    if (!current || current.key !== key) throw new DOMException('Run page closed', 'AbortError');
    const response = await api.control(sessionId, command, { signal: current.controller.signal });
    if (response.data?.data?.receipt?.command_id !== command.command_id) throw new Error('Workflow command acknowledgement is unavailable');
  }), [api, sessionId, key, refresh]);

  useEffect(() => {
    if (!embedded) return;
    document.documentElement.classList.add('workflow-run-embed');
    return () => document.documentElement.classList.remove('workflow-run-embed');
  }, [embedded]);

  useEffect(() => {
    const current = { key, controller: new AbortController() };
    lifetime.current = current;
    latest.current = undefined;
    setSnapshot(undefined);
    setError('');
    setNotice('');
    // Subscribe before the baseline read. Both paths refresh the same versioned snapshot.
    const reload = () => {
      void refresh().catch(reason => { if (!current.controller.signal.aborted) setError(reason instanceof Error ? reason.message : translate.current('chat.workflowRunLoadFailed')); });
    };
    const unsubscribe = watchWorkflowRun(sessionId, reload);
    reload();
    return () => { current.controller.abort(); unsubscribe(); if (lifetime.current === current) lifetime.current = undefined; setSession(key, null); };
  }, [sessionId, refresh, key, setSession]);

  const act = async (intent: WorkflowActionIntent) => {
    setError(''); setNotice('');
    try {
      await actions.execute(intent);
      if (lifetime.current?.key === key && !lifetime.current.controller.signal.aborted) setNotice(t(intent.kind === 'save' ? 'chat.workflowControlSaved' : 'chat.workflowControlAccepted'));
    } catch (reason) {
      if (lifetime.current?.key !== key || lifetime.current.controller.signal.aborted) return;
      if (reason instanceof ReviewRefreshRequired) setNotice(t('chat.workflowControlReviewChanged'));
      else {
        const payload = (reason as { response?: { data?: { error?: { code?: string; message?: string }; message?: string } } })?.response?.data;
        if (payload?.error?.code === 'REVIEW_VERSION_CONFLICT') setNotice(t('chat.workflowControlReviewChanged'));
        else setError(payload?.error?.message ?? payload?.message ?? (reason instanceof Error ? reason.message : t('chat.workflowRunControlFailed')));
      }
      void refresh().catch(() => {});
    }
  };
  const control = snapshot?.control;
  const delivery = control?.delivery;
  const deliveryLabel = delivery && !delivery.consumed_at ? ({ pending: 'chat.workflowControlDeliveryPending', dispatching: 'chat.workflowControlDeliveryPending',
    accepted: delivery.kind === 'cancel' ? 'chat.workflowControlCancellationAccepted' : 'chat.workflowControlDeliveryAccepted', unknown: 'chat.workflowControlDeliveryUnknown', failed: 'chat.workflowControlDeliveryFailed' } as Record<string, string>)[delivery.status] : undefined;

  return <main className={embedded ? 'workflow-run workflow-run--embedded' : 'workflow-run'}
    style={embedded ? undefined : { maxWidth: 1200, margin: '24px auto', padding: 24 }}>
    {!embedded && <h1>{t('chat.workflowPanelTitle')}</h1>}
    {error && <Alert type='error' showIcon message={error} />}
    {notice && <Alert type='info' showIcon message={notice} />}
    {deliveryLabel && <Alert type={delivery?.status === 'unknown' || delivery?.status === 'failed' ? 'warning' : 'info'} showIcon message={t(deliveryLabel)} />}
    {!snapshot && !error && <Spin />}
    {snapshot && <>
      {!embedded && <Button onClick={() => { void refresh().catch(reason => setError(String(reason))); }}>{t('chat.workflowRunRefresh')}</Button>}
      <WorkflowPanel conversationId={key} onRefresh={() => refresh().then(() => {})}
        embedded={embedded}
        controlled control={control} onControl={act} statusLabel={control ? t(controlStatusKey(control)) : undefined} />
    </>}
  </main>;
}
