import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, message, Modal, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';
import { BellOutlined, FileTextOutlined, SafetyCertificateOutlined, WarningOutlined } from '@ant-design/icons';
import { getTask } from '@/modules/taskCenter/api';
import RuleEditor from './RuleEditor';
import { NOTIFICATION_PREFERENCES_CHANGED_EVENT, subscribeNotificationChange } from './notificationEvents';
import { AgentAppsAuth } from '@/components/auth';
import { channelGateEnabled, channelSwitchDefaults, channels, events, getPreferences, notificationError, patchPreferences, ruleError, type ChannelName, type NotificationConfig, type Preferences } from './api';
import { affectedNotificationTasks } from './affectedNotificationTasks';
import { showDesktopNotificationPermission } from './DesktopNotificationPermission';

const taskStatusKeys: Record<string, string> = {
  pending: 'statusPending', running: 'statusRunning', waiting: 'statusWaiting', waiting_inputs: 'statusWaitingInputs',
  succeeded: 'statusSuccess', failed: 'statusFailed', canceled: 'statusCanceled',
};
type SaveResult = { saved: boolean; runningTaskIds?: string[] };
type CloseScope = 'global' | 'desktop' | 'channel' | 'defaults';

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
  const reviewing = useRef(false);
  const closeDialog = useRef<ReturnType<typeof Modal.confirm>>();
  const mounted = useRef(true);
  const loadGeneration = useRef(0);
  const pendingReload = useRef(false);
  const load = useCallback(async () => {
    if (saving.current || reviewing.current) { pendingReload.current = true; return; }
    const generation = ++loadGeneration.current, session = AgentAppsAuth.getSessionIdentity();
    const current = () => mounted.current && generation === loadGeneration.current && session === AgentAppsAuth.getSessionIdentity();
    setBusy(true); setError('');
    try { const p = await getPreferences(); if (current()) { setPrefs(p); setDraft(channelSwitchDefaults(p.defaults)); setConflict(false); } }
    catch { if (current()) setError('loadFailed'); } finally { if (current()) setBusy(false); }
  }, []);
  useEffect(() => {
    mounted.current = true; void load();
    const unsubscribe = subscribeNotificationChange(NOTIFICATION_PREFERENCES_CHANGED_EVENT, () => { void load(); }, true);
    return () => { mounted.current = false; loadGeneration.current += 1; closeDialog.current?.destroy(); unsubscribe(); };
  }, [load]);
  const save = async (patch: Partial<Preferences>, confirmed?: string[], revision = prefs?.revision, enabledChannel?: ChannelName): Promise<SaveResult> => {
    if (revision === undefined || saving.current) return { saved: false };
    const session = AgentAppsAuth.getSessionIdentity();
    const current = () => mounted.current && session === AgentAppsAuth.getSessionIdentity();
    loadGeneration.current += 1;
    saving.current = true; pendingPatch.current = patch; setBusy(true); setError('');
    try {
      const result = await patchPreferences({ ...patch, revision, ...(confirmed ? { confirm_running_task_ids: confirmed } : {}) });
      if (current()) {
        setPrefs(result); setDraft(channelSwitchDefaults(result.defaults)); pendingPatch.current = undefined;
        setConflict(false);
        if (prefs?.enabled && !result.enabled) message.warning(t('notifications.paused'), 2);
        else message.success(t('notifications.saved'));
        if (prefs && !channelGateEnabled(prefs, 'desktop') && channelGateEnabled(result, 'desktop')) showDesktopNotificationPermission(t);
        if (enabledChannel && channelGateEnabled(result, enabledChannel)) onChannelEnabled?.(result);
      }
      return { saved: true };
    } catch (e) {
      if (!current()) return { saved: false };
      const detail = notificationError(e);
      if (enabledChannel) {
        pendingPatch.current = undefined;
        setError(detail.reason === 'NOTIFICATION_CONFIG_CONFLICT' ? 'conflict' : detail.reason);
        try {
          const latest = await getPreferences();
          if (current()) { setPrefs(latest); setDraft(channelSwitchDefaults(latest.defaults)); setConflict(false); }
        } catch { if (current()) setConflict(true); }
        return { saved: false };
      }
      if (detail.reason === 'NOTIFICATION_CONFIRMATION_REQUIRED' && detail.running_task_ids) {
        return { saved: false, runningTaskIds: detail.running_task_ids };
      } else if (detail.reason === 'NOTIFICATION_CONFIG_CONFLICT') { setConflict(true); setError('conflict'); }
      else setError(detail.reason);
      return { saved: false };
    } finally {
      saving.current = false;
      if (mounted.current && !reviewing.current) {
        setBusy(false);
        if (!current()) pendingPatch.current = undefined;
        if (pendingReload.current || !current()) { pendingReload.current = false; void load(); }
      }
    }
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
    else void confirmClose(t('notifications.confirmGlobal'), { enabled: false }, 'global');
  };
  const confirmClose = async (title: string, patch: Partial<Preferences>, scope: CloseScope) => {
    if (!prefs || reviewing.current || saving.current) return;
    const session = AgentAppsAuth.getSessionIdentity();
    const current = () => mounted.current && session === AgentAppsAuth.getSessionIdentity();
    const revision = prefs.revision;
    reviewing.current = true;
    loadGeneration.current += 1;
    setBusy(true); setError('');
    const finish = () => {
      reviewing.current = false; closeDialog.current = undefined;
      if (mounted.current) {
        setBusy(false);
        if (!current()) pendingPatch.current = undefined;
        if (pendingReload.current || !current()) { pendingReload.current = false; void load(); }
      }
    };
    try {
      let affected = await affectedNotificationTasks(prefs, patch.defaults);
      if (!current()) { finish(); return; }
      let confirmedIds = affected.map(item => item.task.id);
      let submitting = false;
      const content = (notice?: string) => <div className="notification-close-content">
          {notice && <Alert type="warning" showIcon message={t('notifications.' + notice)} />}
          <div className="notification-close-warning"><WarningOutlined /><div><strong>{t('notifications.runningTaskCount', { count: affected.length })}</strong><p>{t('notifications.closeReviewHint')}</p></div></div>
          {affected.length > 0 && <div className="notification-close-tasks">{affected.map(({ task, channels: taskChannels }) => <div className="notification-close-task" key={task.id}><FileTextOutlined /><div><strong>{task.title || task.schedule_name || t('notifications.unnamedTask')}</strong><small>{taskChannels.map(channel => t('notifications.' + channel)).join('、')}</small></div><span>{t('taskCenter.' + taskStatusKeys[task.status], { defaultValue: task.status })}</span></div>)}</div>}
          <p className="notification-close-explanation">{t(`notifications.${scope === 'global' ? 'closeGlobalExplanation' : scope === 'desktop' ? 'closeDesktopExplanation' : scope === 'channel' ? 'closeChannelExplanation' : 'closeDefaultsExplanation'}`)}</p>
          <div className="notification-close-safe"><SafetyCertificateOutlined />{t('notifications.closeSafeHint')}</div>
        </div>;
      const dialog = Modal.confirm({
        zIndex: 1600, width: 520, className: 'notification-close-confirm', title, icon: null,
        content: content(),
        okText: t('notifications.confirmClose'),
        cancelText: t('notifications.cancel'),
        afterClose: finish,
        onOk: (close: () => void) => {
          if (submitting) return;
          if (!current()) { close(); return; }
          submitting = true;
          dialog.update({ okButtonProps: { loading: true, disabled: true }, cancelButtonProps: { disabled: true }, keyboard: false });
          // Explicitly close only after saving. A new affected run refreshes
          // this review without rejecting Ant Design's onOk promise.
          void (async () => {
            let keepOpen = true;
            let canConfirm = true;
            try {
              const result = await save(patch, confirmedIds, revision);
              if (!current() || !result.runningTaskIds) { keepOpen = false; close(); return; }
              // A new run started during confirmation. Resolve its name
              // instead of presenting raw IDs in a second dialog.
              const refreshed = await affectedNotificationTasks(prefs, patch.defaults);
              const byId = new Map(refreshed.map(item => [item.task.id, item]));
              affected = await Promise.all(result.runningTaskIds.map(async id => byId.get(id) ?? {
                task: await getTask(id), channels: [],
              }));
              if (!current()) { keepOpen = false; close(); return; }
              confirmedIds = result.runningTaskIds;
              dialog.update({ content: content('confirmationChanged') });
            } catch {
              canConfirm = false;
              if (current()) dialog.update({ content: content('confirmationLoadFailed') });
              else { keepOpen = false; close(); }
            } finally {
              submitting = false;
              if (keepOpen && current()) dialog.update({ okButtonProps: { loading: false, disabled: !canConfirm }, cancelButtonProps: { disabled: false }, keyboard: true });
            }
          })();
        },
      });
      closeDialog.current = dialog;
    } catch { if (current()) setError('loadFailed'); finish(); }
  };
  const changeRule = (next: NotificationConfig) => {
    next = channelSwitchDefaults(next);
    const problem = ruleError(next, true);
    if (problem) { setError(problem); return; }
    const closedChannel = channels.find(channel => prefs?.defaults.channels[channel]?.enabled && !next.channels[channel]?.enabled);
    const closedEvent = events.find(event => prefs?.defaults.events[event].enabled && !next.events[event].enabled);
    if (requestedChannel && prefs && !draft?.channels[requestedChannel]?.enabled && next.channels[requestedChannel]?.enabled) {
      confirmEnable({ defaults: next, ...(!prefs.enabled ? { enabled: true } : {}) }, requestedChannel);
      return;
    }
    const apply = () => { setDraft(next); void save({ defaults: next }); };
    if (closedChannel || closedEvent) {
      const label = t(`notifications.${closedChannel || closedEvent}`);
      void confirmClose(t('notifications.closeNamedTitle', { name: label }), { defaults: next }, closedChannel === 'desktop' ? 'desktop' : closedChannel ? 'channel' : 'defaults');
      return;
    }
    apply();
  };
  return <div className="notification-settings">
    <header className="notification-heading"><BellOutlined /><div><h2>{t('notifications.title')}</h2><p>{t('notifications.subtitle')}</p></div></header>
    {error && <Alert type="error" showIcon message={t('notifications.' + error, { defaultValue: t('notifications.saveFailed') })} action={<Button disabled={busy} onClick={() => {
      if (conflict || !pendingPatch.current) void load();
      else if (pendingPatch.current.enabled === false) changeMaster(false);
      else if (pendingPatch.current.defaults) changeRule(pendingPatch.current.defaults);
      else void save(pendingPatch.current);
    }}>{t('notifications.' + (!conflict && pendingPatch.current ? 'retry' : 'reload'))}</Button>} />}
    {!prefs ? busy ? <Spin /> : null : <>
      {requestedChannel && <Alert className="notification-enable-context" type="info" showIcon message={t('notifications.' + (channelGateEnabled(prefs, requestedChannel) ? 'channelReadyContext' : 'enableChannelContext'), { channel: t('notifications.' + requestedChannel) })} action={channelGateEnabled(prefs, requestedChannel) && <Button disabled={busy || conflict} onClick={() => onChannelEnabled?.(prefs)}>{t('notifications.returnUseChannel')}</Button>} />}
      <section className="notification-surface notification-global-control"><div className="notification-row"><span className="notification-global-icon"><BellOutlined /></span><div className="notification-grow"><strong>{t('notifications.global')}</strong><p>{t('notifications.globalHint')}</p></div><Switch aria-label={t('notifications.global')} checked={prefs.enabled} loading={busy} disabled={busy || conflict} onChange={changeMaster} /></div></section>
      <p className="notification-note">{t('notifications.defaultsHint')}</p>
      {draft && <RuleEditor value={draft} onChange={changeRule} disabled={busy || conflict} preferences={prefs} highlightedChannel={requestedChannel} connectionInPlace={connectionInPlace} />}
    </>}
  </div>;
}
