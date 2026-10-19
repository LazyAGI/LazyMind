import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';
import { BellOutlined, FileTextOutlined, SafetyCertificateOutlined, WarningOutlined } from '@ant-design/icons';
import { listTasks, type Task } from '@/modules/taskCenter/api';
import RuleEditor from './RuleEditor';
import { channelGateEnabled, channelSwitchDefaults, channels, events, getExecutionNotifications, getPreferences, notificationError, patchPreferences, ruleError, type ChannelName, type NotificationConfig, type Preferences } from './api';

const activeTaskStatuses = new Set(['pending', 'running', 'waiting', 'waiting_inputs']);

export default function NotificationSettings({ requestedChannel, onChannelEnabled, onBusyChange, connectionInPlace = false }: {
  requestedChannel?: ChannelName; onChannelEnabled?: (preferences: Preferences) => void;
  onBusyChange?: (busy: boolean) => void; connectionInPlace?: boolean;
}) {
  const { t } = useTranslation();
  const [prefs, setPrefs] = useState<Preferences>();
  const [draft, setDraft] = useState<NotificationConfig>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { onBusyChange?.(busy); }, [busy, onBusyChange]);
  useEffect(() => () => onBusyChange?.(false), [onBusyChange]);
  const [conflict, setConflict] = useState(false);
  const pendingPatch = useRef<Partial<Preferences>>();
  const saving = useRef(false);
  const mounted = useRef(true);
  const load = useCallback(async () => {
    setBusy(true); setError('');
    try { const p = await getPreferences(); if (mounted.current) { setPrefs(p); setDraft(channelSwitchDefaults(p.defaults)); setConflict(false); } }
    catch { if (mounted.current) setError('loadFailed'); } finally { if (mounted.current) setBusy(false); }
  }, []);
  useEffect(() => { mounted.current = true; void load(); return () => { mounted.current = false; }; }, [load]);
  const save = async (patch: Partial<Preferences>, confirmed?: string[], revision = prefs?.revision, enabledChannel?: ChannelName) => {
    if (revision === undefined || saving.current) return;
    saving.current = true; pendingPatch.current = patch; setBusy(true); setError('');
    try {
      const result = await patchPreferences({ ...patch, revision, ...(confirmed ? { confirm_running_task_ids: confirmed } : {}) });
      if (mounted.current) {
        setPrefs(result); setDraft(channelSwitchDefaults(result.defaults)); pendingPatch.current = undefined;
        if (enabledChannel && channelGateEnabled(result, enabledChannel)) onChannelEnabled?.(result);
      }
    } catch (e) {
      if (!mounted.current) return;
      const detail = notificationError(e);
      if (enabledChannel) {
        pendingPatch.current = undefined;
        setError(detail.reason === 'NOTIFICATION_CONFIG_CONFLICT' ? 'conflict' : detail.reason);
        try {
          const current = await getPreferences();
          if (mounted.current) { setPrefs(current); setDraft(channelSwitchDefaults(current.defaults)); setConflict(false); }
        } catch { if (mounted.current) setConflict(true); }
        return;
      }
      if (detail.reason === 'NOTIFICATION_CONFIRMATION_REQUIRED' && detail.running_task_ids) {
        Modal.confirm({ zIndex: 1600, title: t('notifications.confirmGlobal'), content: <><p>{t('notifications.confirmGlobalHint')}</p>{detail.running_task_ids.map(id => <p key={id}>{id}</p>)}</>, okText: t('notifications.off'), cancelText: t('notifications.cancel'), onOk: () => save(patch, detail.running_task_ids, revision) });
      } else if (detail.reason === 'NOTIFICATION_CONFIG_CONFLICT') { setConflict(true); setError('conflict'); }
      else setError(detail.reason);
    } finally { saving.current = false; if (mounted.current) setBusy(false); }
  };
  const confirmEnable = (patch: Partial<Preferences>, channel: ChannelName) => {
    if (!prefs) return;
    const revision = prefs.revision;
    Modal.confirm({
      zIndex: 1600,
      title: t('notifications.enableChannelTitle', { channel: t('notifications.' + channel) }),
      content: <><p>{t('notifications.enableChannelHint')}</p>{!prefs.enabled && <p>{t('notifications.enableMasterHint')}</p>}{channel === 'desktop' && <p>{t('notifications.desktopHint')}</p>}</>,
      okText: t('notifications.enableChannel'), cancelText: t('notifications.cancel'),
      onOk: () => save(patch, undefined, revision, channel),
    });
  };
  const changeMaster = (enabled: boolean) => {
    if (enabled && requestedChannel && prefs) {
      confirmEnable({ enabled: true, ...(!prefs.defaults.channels[requestedChannel]?.enabled ? {
        defaults: { ...prefs.defaults, channels: { ...prefs.defaults.channels, [requestedChannel]: { enabled: true } } },
      } : {}) }, requestedChannel);
    } else if (enabled) void save({ enabled });
    else void confirmClose(t('notifications.confirmGlobal'), ids => void save({ enabled: false }, ids), undefined, 'global');
  };
  const affectedTasks = async (next?: NotificationConfig): Promise<Array<{ task: Task; channels: string[] }>> => {
    if (!draft) return [];
    const closedEvents = next ? events.filter(event => draft.events[event].enabled && !next.events[event].enabled) : [];
    const closedChannels = next ? channels.filter(channel => draft.channels[channel]?.enabled && !next.channels[channel]?.enabled) : channels;
    const desktopAffected = closedChannels.includes('desktop') && Boolean(draft.channels.desktop?.enabled);
    const affected: Array<{ task: Task; channels: string[] }> = [];
    for (let page = 1; page <= 100; page += 1) {
      const result = await listTasks({ ...(desktopAffected ? {} : { task_type: 'scheduled' }), page, page_size: 100 });
      for (const task of result.items.filter(item => activeTaskStatuses.has(item.status))) {
        if (desktopAffected && task.task_type !== 'scheduled' && !task.schedule_id) {
          affected.push({ task, channels: [t('notifications.desktop')] });
          continue;
        }
        const { snapshot } = await getExecutionNotifications(task.id);
        const config = snapshot.config;
        if (!config) {
          if (desktopAffected) affected.push({ task, channels: [t('notifications.desktop')] });
          continue;
        }
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
  const confirmClose = async (title: string, apply: (taskIds: string[]) => void, next?: NotificationConfig, scope: 'global' | 'desktop' | 'channel' | 'defaults' = 'defaults') => {
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
          <p className="notification-close-explanation">{t(`notifications.${scope === 'global' ? 'closeGlobalExplanation' : scope === 'desktop' ? 'closeDesktopExplanation' : scope === 'channel' ? 'closeChannelExplanation' : 'closeDefaultsExplanation'}`)}</p>
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
    next = channelSwitchDefaults(next);
    const problem = ruleError(next, true);
    if (problem) { setError(problem); return; }
    const closedChannel = channels.find(channel => draft?.channels[channel]?.enabled && !next.channels[channel]?.enabled);
    const closedEvent = events.find(event => draft?.events[event].enabled && !next.events[event].enabled);
    if (requestedChannel && prefs && !draft?.channels[requestedChannel]?.enabled && next.channels[requestedChannel]?.enabled) {
      confirmEnable({ defaults: next, ...(!prefs.enabled ? { enabled: true } : {}) }, requestedChannel);
      return;
    }
    const apply = () => { setDraft(next); void save({ defaults: next }); };
    if (closedChannel || closedEvent) {
      const label = t(`notifications.${closedChannel || closedEvent}`);
      void confirmClose(t('notifications.closeNamedTitle', { name: label }), apply, next, closedChannel === 'desktop' ? 'desktop' : closedChannel ? 'channel' : 'defaults');
      return;
    }
    apply();
  };
  return <div className="notification-settings">
    <header className="notification-heading"><BellOutlined /><div><h2>{t('notifications.title')}</h2><p>{t('notifications.subtitle')}</p></div></header>
    {error && <Alert type="error" showIcon message={t('notifications.' + error, { defaultValue: t('notifications.saveFailed') })} action={<Button disabled={busy} onClick={() => { if (!conflict && pendingPatch.current) void save(pendingPatch.current); else void load(); }}>{t('notifications.' + (!conflict && pendingPatch.current ? 'retry' : 'reload'))}</Button>} />}
    {!prefs ? busy ? <Spin /> : null : <>
      {requestedChannel && <Alert className="notification-enable-context" type="info" showIcon message={t('notifications.' + (channelGateEnabled(prefs, requestedChannel) ? 'channelReadyContext' : 'enableChannelContext'), { channel: t('notifications.' + requestedChannel) })} action={channelGateEnabled(prefs, requestedChannel) && <Button disabled={busy || conflict} onClick={() => onChannelEnabled?.(prefs)}>{t('notifications.returnUseChannel')}</Button>} />}
      <section className="notification-surface notification-global-control"><div className="notification-row"><span className="notification-global-icon"><BellOutlined /></span><div className="notification-grow"><strong>{t('notifications.global')}</strong><p>{t('notifications.globalHint')}</p></div><Switch aria-label={t('notifications.global')} checked={prefs.enabled} loading={busy} disabled={busy || conflict} onChange={changeMaster} /></div></section>
      {!prefs.enabled && <Alert type="warning" showIcon message={t('notifications.paused')} />}
      <p className="notification-note">{t('notifications.defaultsHint')}</p>
      {draft && <RuleEditor value={draft} onChange={changeRule} disabled={busy || conflict} highlightedChannel={requestedChannel} connectionInPlace={connectionInPlace} />}
    </>}
  </div>;
}
