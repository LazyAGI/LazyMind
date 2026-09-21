import { useExecutionActivity } from '@/modules/chat/components/WorkflowPanel/external/useExecutionActivity';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Spin } from 'antd';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AgentAppsAuth } from '@/components/auth';
import { WorkflowPanel } from '@/modules/chat/components/WorkflowPanel';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { CONTROL_NOTICE_TTL_MS, controlActions, controlNoticeKey, deliveryBanner, overlayInfoBanner, REVIEW_CHANGED_NOTICE, ReviewRefreshRequired, type WorkflowActionIntent } from '@/modules/chat/utils/workflowControl';
import { loadWorkflowRunSnapshot, watchWorkflowRun, type WorkflowRunSnapshot } from './loadRun';
import './index.scss';

/** Shared run workbench: user intent goes to Core, never to an interpreted chat prompt. */
export default function WorkflowRunPage({ embedded = false }: { embedded?: boolean }) {
  const { sessionId = '' } = useParams();
  const { t } = useTranslation();
  const [hostExpanded, setHostExpanded] = useState(false);
  const [hostCollapsed, setHostCollapsed] = useState(false);
  const hostOrigin = useMemo(() => {
    const value = new URLSearchParams(window.location.search).get('hostOrigin');
    try { const url = new URL(value ?? ''); return ['http:', 'https:'].includes(url.protocol) ? url.origin : undefined; }
    catch { return undefined; }
  }, []);
  const toggleHostExpand = useCallback(() => {
    if (hostOrigin) window.parent.postMessage({ type: 'lazymind.workflow.toggle-expand', sessionId }, hostOrigin);
  }, [hostOrigin, sessionId]);
  const toggleHostCollapse = useCallback(() => {
    if (hostOrigin) window.parent.postMessage({ type: 'lazymind.workflow.toggle-collapse', sessionId }, hostOrigin);
  }, [hostOrigin, sessionId]);
  useEffect(() => {
    if (!embedded || !hostOrigin) return;
    const receive = (event: MessageEvent) => {
      if (event.source === window.parent && event.origin === hostOrigin
        && event.data?.type === 'lazymind.workflow.expansion' && event.data.sessionId === sessionId
        && typeof event.data.expanded === 'boolean') setHostExpanded(event.data.expanded);
      if (event.source === window.parent && event.origin === hostOrigin
        && event.data?.type === 'lazymind.workflow.collapse' && event.data.sessionId === sessionId
        && typeof event.data.collapsed === 'boolean') setHostCollapsed(event.data.collapsed);
    };
    const keydown = (event: KeyboardEvent) => { if (event.key === 'Escape' && hostExpanded) toggleHostExpand(); };
    window.addEventListener('message', receive);
    window.addEventListener('keydown', keydown);
    return () => { window.removeEventListener('message', receive); window.removeEventListener('keydown', keydown); };
  }, [embedded, hostOrigin, sessionId, hostExpanded, toggleHostExpand]);

  const user = AgentAppsAuth.getUserInfo();
  const key = `workflow-run:${window.location.origin}:${user?.tenantId ?? user?.tenant_id ?? ''}:${user?.userId ?? user?.username ?? ''}:${sessionId}`;
  const [error, setError] = useState('');
  const [syncError, setSyncError] = useState('');
  const [noticeKey, setNoticeKey] = useState('');
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
      setSyncError('');
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
    setSyncError('');
    setNoticeKey('');
    // Subscribe before the baseline read. Both paths refresh the same versioned snapshot.
    const reload = () => {
      void refresh().catch(reason => { if (!current.controller.signal.aborted) setSyncError(reason instanceof Error ? reason.message : translate.current('chat.workflowRunLoadFailed')); });
    };
    const unsubscribe = watchWorkflowRun(sessionId, reload);
    reload();
    return () => { current.controller.abort(); unsubscribe(); if (lifetime.current === current) lifetime.current = undefined; setSession(key, null); };
  }, [sessionId, refresh, key, setSession]);

  const act = async (intent: WorkflowActionIntent) => {
    setError(''); setNoticeKey('');
    try {
      await actions.execute(intent);
      if (lifetime.current?.key === key && !lifetime.current.controller.signal.aborted) setNoticeKey(controlNoticeKey(intent.kind));
    } catch (reason) {
      if (lifetime.current?.key !== key || lifetime.current.controller.signal.aborted) return;
      if (reason instanceof ReviewRefreshRequired) setNoticeKey(REVIEW_CHANGED_NOTICE);
      else {
        const payload = (reason as { response?: { data?: { error?: { code?: string; message?: string }; message?: string } } })?.response?.data;
        if (payload?.error?.code === 'REVIEW_VERSION_CONFLICT') setNoticeKey(REVIEW_CHANGED_NOTICE);
        else setError(payload?.error?.message ?? payload?.message ?? (reason instanceof Error ? reason.message : t('chat.workflowRunControlFailed')));
      }
      void refresh().catch(() => {});
    }
  };
  useEffect(() => {
    if (!noticeKey || noticeKey === REVIEW_CHANGED_NOTICE) return;
    const timer = window.setTimeout(() => setNoticeKey(''), CONTROL_NOTICE_TTL_MS);
    return () => window.clearTimeout(timer);
  }, [noticeKey]);
  const activities = useExecutionActivity(snapshot?.session);
  const control = snapshot?.control;
  const liveDelivery = deliveryBanner(control?.delivery);
  const infoBanner = overlayInfoBanner(noticeKey, control?.delivery);
  const bannerFromNotice = Boolean(infoBanner && (!liveDelivery || noticeKey === REVIEW_CHANGED_NOTICE));

  return <main className={embedded ? 'workflow-run workflow-run--embedded' : 'workflow-run'}
    style={embedded ? undefined : { maxWidth: 1200, margin: '24px auto', padding: 24 }}>
    {!embedded && <h1>{t('chat.workflowPanelTitle')}</h1>}
    {error && <Alert type='error' showIcon closable onClose={() => setError('')} message={error} />}
    {syncError && <Alert type='warning' showIcon message={t('chat.workflowRunSyncFailed')} description={syncError} />}
    {control?.continuation === 'binding_required' && <Alert type='warning' showIcon
      message={t('chat.workflowHostBindingRequiredHint')} />}
    {!error && infoBanner && <Alert type={infoBanner.tone} showIcon
      closable={bannerFromNotice} onClose={() => setNoticeKey('')} message={t(infoBanner.key)} />}
    {!snapshot && !error && !syncError && <Spin />}
    {snapshot && <>
      {!embedded && <Button onClick={() => { void refresh().catch(reason => setError(String(reason))); }}>{t('chat.workflowRunRefresh')}</Button>}
      <WorkflowPanel conversationId={key} onRefresh={() => refresh().then(() => {})}
        embedded={embedded} externalPresentation={{ activities, expanded: hostExpanded,
          onToggleExpand: embedded && hostOrigin ? toggleHostExpand : undefined,
          collapsed: embedded ? hostCollapsed : undefined,
          onToggleCollapse: embedded && hostOrigin ? toggleHostCollapse : undefined }}
        controlAdapter={control ? { control, execute: act } : undefined} />
    </>}
  </main>;
}
