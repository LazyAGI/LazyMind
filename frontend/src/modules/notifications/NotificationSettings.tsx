import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';
import { BellOutlined, FileTextOutlined, SafetyCertificateOutlined, WarningOutlined } from '@ant-design/icons';
import { listTasks, type Task } from '@/modules/taskCenter/api';
import RuleEditor from './RuleEditor';
import { channels, events, getExecutionNotifications, getPreferences, notificationError, patchPreferences, ruleError, type NotificationConfig, type Preferences } from './api';

const activeTaskStatuses = new Set(['pending', 'running', 'waiting', 'waiting_inputs']);

export default function NotificationSettings() {
  const { t } = useTranslation();
  const [prefs, setPrefs] = useState<Preferences>();
  const [draft, setDraft] = useState<NotificationConfig>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [conflict, setConflict] = useState(false);
  const pendingPatch = useRef<Partial<Preferences>>();
  const saving = useRef(false);
  const mounted = useRef(true);
  const load = useCallback(async () => {
    setBusy(true); setError('');
    try { const p = await getPreferences(); if (mounted.current) { setPrefs(p); setDraft(p.defaults); setConflict(false); } }
    catch { if (mounted.current) setError('loadFailed'); } finally { if (mounted.current) setBusy(false); }
  }, []);
  useEffect(() => { mounted.current = true; void load(); return () => { mounted.current = false; }; }, [load]);
  const save = async (patch: Partial<Preferences>, confirmed?: string[], revision = prefs?.revision) => {
    if (revision === undefined || saving.current) return;
    saving.current = true; pendingPatch.current = patch; setBusy(true); setError('');
    try {
      const result = await patchPreferences({ ...patch, revision, ...(confirmed ? { confirm_running_task_ids: confirmed } : {}) });
      if (mounted.current) { setPrefs(result); setDraft(result.defaults); pendingPatch.current = undefined; }
    } catch (e) {
      if (!mounted.current) return;
      const detail = notificationError(e);
      if (detail.reason === 'NOTIFICATION_CONFIRMATION_REQUIRED' && detail.running_task_ids) {
        Modal.confirm({ zIndex: 1600, title: t('notifications.confirmGlobal'), content: <><p>{t('notifications.confirmGlobalHint')}</p>{detail.running_task_ids.map(id => <p key={id}>{id}</p>)}</>, okText: t('notifications.off'), cancelText: t('notifications.cancel'), onOk: () => save(patch, detail.running_task_ids, revision) });
      } else if (detail.reason === 'NOTIFICATION_CONFIG_CONFLICT') { setConflict(true); setError('conflict'); }
      else setError(detail.reason);
    } finally { saving.current = false; if (mounted.current) setBusy(false); }
  };
  const affectedTasks = async (next?: NotificationConfig): Promise<Array<{ task: Task; channels: string[] }>> => {
    if (!draft) return [];
    const closedEvents = next ? events.filter(event => draft.events[event].enabled && !next.events[event].enabled) : events;
    const closedChannels = next ? channels.filter(channel => draft.channels[channel]?.enabled && !next.channels[channel]?.enabled) : channels;
    const affected: Array<{ task: Task; channels: string[] }> = [];
    for (let page = 1; page <= 100; page += 1) {
      const result = await listTasks({ task_type: 'scheduled', page, page_size: 100 });
      for (const task of result.items.filter(item => activeTaskStatuses.has(item.status))) {
        const { snapshot } = await getExecutionNotifications(task.id);
        const config = snapshot.config;
        if (!config) continue;
        const eventAffected = closedEvents.some(event => config.events[event].enabled);
        const channelAffected = closedChannels.some(channel => config.channels[channel]?.enabled);
        if (!eventAffected && !channelAffected) continue;
        affected.push({ task, channels: channels.filter(channel => config.channels[channel]?.enabled).map(channel => t(`notifications.${channel}`)) });
      }
      if (page * 100 >= result.total) break;
      if (page === 100) throw new Error('TOO_MANY_RUNS');
    }
    return affected;
  };
  const confirmClose = async (title: string, apply: (taskIds: string[]) => void, next?: NotificationConfig) => {
    setBusy(true); setError('');
    try {
      const affected = await affectedTasks(next);
      Modal.confirm({
        zIndex: 1600,
        width: 520,
        className: 'notification-close-confirm',
        title,
        icon: null,
        content: <div className="notification-close-content">
          <div className="notification-close-warning"><WarningOutlined /><div><strong>{t('notifications.runningTaskCount', { count: affected.length })}</strong><p>{t('notifications.closeReviewHint')}</p></div></div>
          {affected.length > 0 && <div className="notification-close-tasks">{affected.map(({ task, channels: taskChannels }) => <div className="notification-close-task" key={task.id}><FileTextOutlined /><div><strong>{task.title || task.schedule_name || task.id}</strong><small>{taskChannels.join('、')}</small></div><span>{t('notifications.running')}</span></div>)}</div>}
          <p className="notification-close-explanation">{t('notifications.closeDefaultsExplanation')}</p>
          <div className="notification-close-safe"><SafetyCertificateOutlined />{t('notifications.closeSafeHint')}</div>
        </div>,
        okText: t('notifications.confirmClose'),
        cancelText: t('notifications.cancel'),
        onOk: () => apply(affected.map(item => item.task.id)),
      });
    } catch { setError('loadFailed'); }
    finally { if (mounted.current) setBusy(false); }
  };
  const changeRule = (next: NotificationConfig) => {
    const problem = ruleError(next, true);
    if (problem) { setError(problem); return; }
    const closedChannel = channels.find(channel => draft?.channels[channel]?.enabled && !next.channels[channel]?.enabled);
    const closedEvent = events.find(event => draft?.events[event].enabled && !next.events[event].enabled);
    const apply = () => { setDraft(next); void save({ defaults: next }); };
    if (closedChannel || closedEvent) {
      const label = t(`notifications.${closedChannel || closedEvent}`);
      void confirmClose(t('notifications.closeNamedTitle', { name: label }), apply, next);
      return;
    }
    apply();
  };
  return <div className="notification-settings">
    <header className="notification-heading"><BellOutlined /><div><h2>{t('notifications.title')}</h2><p>{t('notifications.subtitle')}</p></div></header>
    {error && <Alert type="error" showIcon message={t('notifications.' + error, { defaultValue: t('notifications.saveFailed') })} action={<Button disabled={busy} onClick={() => { if (!conflict && pendingPatch.current) void save(pendingPatch.current); else void load(); }}>{t('notifications.' + (!conflict && pendingPatch.current ? 'retry' : 'reload'))}</Button>} />}
    {!prefs ? busy ? <Spin /> : null : <>
      <section className="notification-surface notification-global-control"><div className="notification-row"><span className="notification-global-icon"><BellOutlined /></span><div className="notification-grow"><strong>{t('notifications.global')}</strong><p>{t('notifications.globalHint')}</p></div><Switch aria-label={t('notifications.global')} checked={prefs.enabled} loading={busy} disabled={busy || conflict} onChange={(enabled: boolean) => enabled ? void save({ enabled }) : void confirmClose(t('notifications.confirmGlobal'), ids => void save({ enabled: false }, ids))} /></div></section>
      {!prefs.enabled && <Alert type="warning" showIcon message={t('notifications.paused')} />}
      <p className="notification-note">{t('notifications.defaultsHint')}</p>
      {draft && <RuleEditor value={draft} onChange={changeRule} disabled={busy || conflict} />}
    </>}
  </div>;
}
