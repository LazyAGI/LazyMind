import { BellOutlined, FileTextOutlined, RightOutlined, SafetyCertificateOutlined, SettingOutlined, WarningOutlined } from '@ant-design/icons';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Drawer, message, Modal, Select, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { AgentAppsAuth } from '@/components/auth';
import { listScheduleTasks, type Task } from '@/modules/taskCenter/api';
import { channelAccountLabel, type ChannelAccount } from '@/modules/channelGateway/api';
import RuleEditor from './RuleEditor';
import NotificationSummary from './NotificationSummary';
import NotificationSettings from './NotificationSettings';
import NotificationHistory from './NotificationHistory';
import ChannelBrand from './ChannelBrand';
import useWeChatNotificationStatus from './useWeChatNotificationStatus';
import { subscribeNotificationChange } from './notificationEvents';
import { wechatStatusHint, wechatStatusLabel } from './wechatNotificationStatus';
import { availabilityError, channelGateEnabled, channels, events, emptyRule, getAccountDetail, getPreferences, getScheduleNotifications, NOTIFICATION_PREFERENCES_CHANGED_EVENT, notificationError, putScheduleNotifications, ruleError, taskRuleFromDefaults, type ChannelName, type ChannelRule, type AccountDetail, type NotificationConfig, type Preferences, type ScheduleNotifications, type NotificationUpdate } from './api';

type WeChatStatus = ReturnType<typeof useWeChatNotificationStatus>['status'];

function WeChatStatusTag({ status }: { status: WeChatStatus }) {
  const { t } = useTranslation();
  return status ? <Tag className="notification-wechat-status" color={status === 'connected' ? 'success' : 'warning'}>{t('notifications.' + wechatStatusLabel(status))}</Tag> : null;
}

function ConfiguredChannel({ channel, rule, unavailable, blocked, wechatStatus, liveAccount }: { channel: ChannelName; rule: ChannelRule; unavailable: boolean; blocked: boolean; wechatStatus?: WeChatStatus; liveAccount?: ChannelAccount }) {
  const { t } = useTranslation();
  const [account, setAccount] = useState<AccountDetail>();
  useEffect(() => {
    let active = true;
    setAccount(undefined);
    if (rule.account_id) void getAccountDetail(rule.account_id).then(value => { if (active) setAccount(value); }).catch(() => undefined);
    return () => { active = false; };
  }, [rule.account_id]);
  const displayAccount = liveAccount?.id === rule.account_id ? liveAccount : account?.id === rule.account_id ? account : undefined;
  return <div className="notification-row"><ChannelBrand channel={channel} avatar={displayAccount?.avatar_url} /><div className="notification-grow"><strong>{t('notifications.' + channel)}</strong><p>{[displayAccount?.label || rule.account_id, rule.recipient_id].filter(Boolean).join(' · ')}</p></div><Tag>{t('notifications.' + (rule.enabled ? 'on' : 'off'))}</Tag>{channel === 'wechat' && <WeChatStatusTag status={wechatStatus} />}{blocked && <Tag color="warning">{t('notifications.channelPaused')}</Tag>}{unavailable && rule.enabled && !blocked && <Tag color="warning">{t('notifications.unavailable')}</Tag>}</div>;
}

export default function ScheduleNotificationPanel({ scheduleId, taskId: selectedTaskId, compact = false, draftMode = false, showHistory = true, summaryCard = false, editorOnly = false, title, editorOpen, onEditorOpenChange, onDraftChange }: {
  scheduleId?: string; taskId?: string; compact?: boolean; draftMode?: boolean; title?: string;
  showHistory?: boolean;
  summaryCard?: boolean;
  editorOnly?: boolean;
  editorOpen?: boolean; onEditorOpenChange?: (open: boolean) => void; onDraftChange?: (change: NotificationUpdate | undefined) => void;
}) {
  const { t } = useTranslation();
  const [config, setConfig] = useState<ScheduleNotifications>();
  const [draft, setDraft] = useState<NotificationConfig | null>();
  const [preferences, setPreferences] = useState<Preferences>();
  const [preferencesBusy, setPreferencesBusy] = useState(false);
  const [preferencesError, setPreferencesError] = useState(false);
  const enabled = preferences?.enabled ?? true;
  const [internalOpen, setInternalOpen] = useState(false);
  const open = editorOpen ?? internalOpen;
  const setOpen = (value: boolean) => { setInternalOpen(value); onEditorOpenChange?.(value); };
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [accountsRefresh, setAccountsRefresh] = useState(0);
  const [requestedChannel, setRequestedChannel] = useState<ChannelName>();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [errorAccount, setErrorAccount] = useState<{ id: string; label: string }>();
  const [conflict, setConflict] = useState(false);
  const [runs, setRuns] = useState<Task[]>([]);
  const [runPage, setRunPage] = useState(1);
  const [runTotal, setRunTotal] = useState(0);
  const [taskId, setTaskId] = useState<string>();
  const loadGeneration = useRef(0);
  const preferencesGeneration = useRef(0);
  useEffect(() => {
    setPreferencesBusy(false);
    return () => { loadGeneration.current += 1; preferencesGeneration.current += 1; };
  }, [scheduleId]);
  const load = useCallback(async () => {
    const generation = ++loadGeneration.current;
    const session = AgentAppsAuth.getSessionIdentity();
    const currentRequest = () => loadGeneration.current === generation && session === AgentAppsAuth.getSessionIdentity();
    setLoading(true); setError(''); setErrorAccount(undefined);
    try {
      const [c, p] = await Promise.all([scheduleId ? getScheduleNotifications(scheduleId) : Promise.resolve(undefined), getPreferences()]);
      if (!currentRequest()) return;
      const current = c || { revision: 0, configured: true, config: taskRuleFromDefaults(p.defaults), availability: {} };
      // Core availability combines upstream failures into a provider reason.
      // The selected WeChat target is checked separately before showing repair
      // guidance, so a temporary network failure is never called activation.
      const reportedIssue = availabilityError(current, p);
      const issue = reportedIssue?.provider === 'wechat' ? undefined : reportedIssue;
      setConfig(current); setDraft(current.config); setPreferences(previous => previous && previous.revision > p.revision ? previous : p); setPreferencesError(false); setConflict(false);
      if (issue?.account_id) {
        void getAccountDetail(issue.account_id).then(account => { if (currentRequest()) setErrorAccount({ id: issue.account_id, label: channelAccountLabel(account) }); }).catch(() => { if (currentRequest()) setErrorAccount({ id: issue.account_id, label: issue.provider }); });
      }
    } catch { if (currentRequest()) setError('loadFailed'); } finally { if (currentRequest()) setLoading(false); }
  }, [scheduleId]);
  useEffect(() => { if (!compact || draftMode || summaryCard) void load(); }, [load, compact, draftMode, summaryCard]);
  useEffect(() => { if (compact && !draftMode && !summaryCard && open) void load(); }, [load, compact, draftMode, summaryCard, open]);
  const refreshPreferences = useCallback(async () => {
    const generation = ++preferencesGeneration.current;
    const session = AgentAppsAuth.getSessionIdentity();
    const currentRequest = () => preferencesGeneration.current === generation && session === AgentAppsAuth.getSessionIdentity();
    setPreferencesBusy(true);
    try {
      const current = await getPreferences();
      if (!currentRequest()) return undefined;
      setPreferences(previous => previous && previous.revision > current.revision ? previous : current); setPreferencesError(false);
      return current;
    } catch (error) {
      if (!currentRequest()) return undefined;
      setPreferences(undefined); setPreferencesError(true);
      throw error;
    } finally { if (currentRequest()) setPreferencesBusy(false); }
  }, []);
  useEffect(() => {
    const changed = () => { void refreshPreferences().catch(() => setError('loadFailed')); };
    return subscribeNotificationChange(NOTIFICATION_PREFERENCES_CHANGED_EVENT, changed, true);
  }, [refreshPreferences]);
  useEffect(() => {
    if (open && (!compact || draftMode)) void refreshPreferences().catch(() => setError('loadFailed'));
  }, [open, compact, draftMode, refreshPreferences]);
  useEffect(() => {
    if (compact || !scheduleId || !showHistory) return;
    let active = true;
    listScheduleTasks(scheduleId, runPage).then(page => { if (active) { setRuns(old => runPage === 1 ? page.items : [...old, ...page.items]); setRunTotal(page.total); if (runPage === 1) setTaskId(page.items[0]?.id); } }).catch(() => { if (active) setError('loadFailed'); });
    return () => { active = false; };
  }, [scheduleId, compact, runPage, showHistory]);
  const save = async (value = draft) => {
    if (!config || value === undefined || saving) return;
    const reason = value ? ruleError(value, false, preferences ? { preferences, original: config.config } : undefined) : undefined;
    if (reason) {
      setError(reason);
      if (reason === 'NOTIFICATION_TARGET_REQUIRED' && value?.channels.wecom?.enabled && !value.channels.wecom.recipient_id) {
        message.error(t('notifications.selectWecomRecipientHint'), 6);
      }
      return;
    }
    setSaving(true); setError('');
    try {
      if (draftMode) {
        onDraftChange?.({ revision: config.revision, ...(value === null ? { clear: true } : { config: value }) });
        setConfig({ ...config, config: value, configured: value !== null });
      } else if (scheduleId) setConfig(await putScheduleNotifications(scheduleId, config.revision, value));
      setOpen(false);
    }
    catch (e) {
      const detail = notificationError(e);
      const reason = detail.reason === 'NOTIFICATION_CONFIG_CONFLICT' ? 'conflict' : detail.reason;
      setError(reason); setConflict(detail.reason === 'NOTIFICATION_CONFIG_CONFLICT');
      const provider = ({
        WECHAT_NOTIFICATION_CONTEXT_REQUIRED: 'wechat',
        WECOM_NOTIFICATION_TARGET_UNAVAILABLE: 'wecom',
        FEISHU_NOTIFICATION_TARGET_UNAVAILABLE: 'feishu',
      } as const)[detail.reason as 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED' | 'WECOM_NOTIFICATION_TARGET_UNAVAILABLE' | 'FEISHU_NOTIFICATION_TARGET_UNAVAILABLE'];
      const accountId = provider && value?.channels[provider]?.account_id;
      if (accountId && provider) void getAccountDetail(accountId).then(account => setErrorAccount({ id: accountId, label: channelAccountLabel(account) })).catch(() => setErrorAccount({ id: accountId, label: provider }));
    }
    finally { setSaving(false); }
  };
  const change = (next: NotificationConfig | null, apply = () => setDraft(next)) => {
    const closedEvent = draft && events.find(event => draft.events[event].enabled && !next?.events[event].enabled);
    const closedChannel = draft && channels.find(channel => draft.channels[channel]?.enabled && !next?.channels[channel]?.enabled);
    if (!closedEvent && !closedChannel) { apply(); return; }
    const label = t(`notifications.${closedChannel || closedEvent}`);
    const taskChannels = channels.filter(channel => draft?.channels[channel]?.enabled).map(channel => t(`notifications.${channel}`));
    Modal.confirm({ zIndex: 1600, width: 520, className: 'notification-close-confirm', icon: null,
      title: t('notifications.closeNamedTitle', { name: label }),
      content: <div className="notification-close-content">
        <div className="notification-close-warning"><WarningOutlined /><div><strong>{t('notifications.currentTaskAffected')}</strong><p>{t('notifications.closeReviewHint')}</p></div></div>
        <div className="notification-close-tasks"><div className="notification-close-task"><FileTextOutlined /><div><strong>{title || t('notifications.newSchedule')}</strong><small>{taskChannels.join('、') || t('notifications.off')}</small></div><span>{t('notifications.associated')}</span></div></div>
        <p className="notification-close-explanation">{t('notifications.closeTaskExplanation')}</p>
        <div className="notification-close-safe"><SafetyCertificateOutlined />{t('notifications.closeSafeHint')}</div>
      </div>,
      okText: t('notifications.confirmClose'), cancelText: t('notifications.cancel'), onOk: apply,
    });
  };
  const discardEditor = () => { setDraft(config?.config); setError(''); setOpen(false); };
  const cancelEditor = () => {
    if (saving || settingsBusy) return;
    if (draft !== undefined && config && JSON.stringify(draft) !== JSON.stringify(config.config)) {
      Modal.confirm({
        zIndex: 1600,
        title: t('notifications.unsavedChangesTitle'),
        content: t('notifications.unsavedChangesHint'),
        okText: t('notifications.discardChanges'),
        okButtonProps: { danger: true },
        cancelText: t('notifications.continueEditing'),
        onOk: discardEditor,
      });
      return;
    }
    discardEditor();
  };
  const openSettings = (channel?: ChannelName) => { setRequestedChannel(channel); setSettingsOpen(true); };
  const closeSettings = () => {
    if (settingsBusy) return;
    setAccountsRefresh(value => value + 1);
    setSettingsOpen(false); setRequestedChannel(undefined); void refreshPreferences().catch(() => setError('loadFailed'));
  };
  const channelEnabled = (current: Preferences) => {
    setAccountsRefresh(value => value + 1);
    setPreferences(current); setPreferencesError(false); setSettingsOpen(false); setSettingsBusy(false);
    if (requestedChannel) setDraft(previous => {
      const value = previous || emptyRule();
      return { ...value, channels: { ...value.channels, [requestedChannel]: { ...value.channels[requestedChannel], enabled: true } } };
    });
    setRequestedChannel(undefined);
  };
  const restoreGlobalDefaults = async () => {
    const generation = loadGeneration.current;
    setLoading(true);
    try {
      const current = await refreshPreferences();
      if (current && loadGeneration.current === generation) setDraft(taskRuleFromDefaults(current.defaults));
    } catch { if (loadGeneration.current === generation) setError('loadFailed'); }
    finally { if (loadGeneration.current === generation) setLoading(false); }
  };
  const reportedIssue = availabilityError(config, preferences);
  const currentError = error || (reportedIssue?.provider !== 'wechat' ? reportedIssue?.reason : undefined);
  const currentErrorAccount = error || errorAccount?.id === reportedIssue?.account_id ? errorAccount?.label : undefined;
  const errorView = currentError && <Alert type="error" message={t('notifications.' + currentError, { account: currentErrorAccount || t('notifications.account'), defaultValue: t('notifications.saveFailed') })} action={<Button onClick={() => { if (preferencesError) void refreshPreferences().then(current => { if (current) setError(''); }).catch(() => setError('loadFailed')); else void load(); }}>{t('notifications.reload')}</Button>} />;
  const activeChannels = channels.filter(channel => config?.config?.channels[channel]?.enabled);
  const blocked = (channel: ChannelName) => Boolean(preferences && !channelGateEnabled(preferences, channel));
  const configuredWechat = config?.configured ? config.config?.channels.wechat : undefined;
  const watchWechat = Boolean(!editorOnly && configuredWechat?.enabled && configuredWechat.account_id && configuredWechat.recipient_id
    && preferences && channelGateEnabled(preferences, 'wechat'));
  const wechat = useWeChatNotificationStatus({
    enabled: watchWechat, ready: !loading && !preferencesBusy, accountId: configuredWechat?.account_id, recipientId: configuredWechat?.recipient_id,
    refreshKey: JSON.stringify([scheduleId, selectedTaskId, config?.revision, accountsRefresh]),
  });
  const wechatStatus = watchWechat && !loading && !preferencesBusy ? wechat.status : undefined;
  const wechatWarning = wechatStatus && !['connected', 'checking', 'unconfigured'].includes(wechatStatus) && <Alert
    className="notification-task-wechat-warning" type="warning" showIcon
    message={t('notifications.' + (wechatStatus === 'pending' ? 'wechatActivationRequired' : wechatStatus === 'targetUnavailable' ? 'wechatTargetUnavailable' : 'wechatConnectionIssue'))}
    description={<>{t('notifications.' + wechatStatusHint(wechatStatus))}{wechat.account?.label && <p>{[wechat.account.label, configuredWechat?.recipient_id].filter(Boolean).join(' · ')}</p>}</>}
    action={['reauthorization', 'disconnected', 'targetUnavailable'].includes(wechatStatus)
      ? <Button aria-label={`${t('notifications.configure')} · ${t('notifications.wechat')}`} onClick={() => { setDraft(config?.config); setOpen(true); }}>{t('notifications.configure')}</Button>
      : <Button onClick={wechat.refresh}>{t('notifications.reload')}</Button>}
  />;
  const summary = config?.configured
    ? activeChannels.length ? `${activeChannels.map(channel => `${t('notifications.' + channel)}${blocked(channel) ? `（${t('notifications.channelPaused')}）` : ''}`).join('、')} · ${events.filter(event => config.config?.events[event].enabled).map(event => t(event === 'succeeded' ? 'taskCenter.statusSuccess' : event === 'failed' ? 'taskCenter.statusFailed' : 'notifications.waiting')).join('、')}` : t('notifications.off')
    : t('notifications.unconfigured');
  return <div className={compact ? 'notification-compact' : 'notification-schedule'}>
    {!editorOnly && <>
    {summaryCard && <>
      <h3>{t('notifications.reminder')}</h3>
      {errorView}
      <button type='button' className='task-notification-entry' aria-label={t('notifications.configure')} disabled={loading || !config} onClick={() => { setDraft(config?.config); setOpen(true); }}>
        <span className='task-notification-entry-icon'><BellOutlined /></span>
        <span className='task-notification-entry-copy'><strong>{summary}</strong><WeChatStatusTag status={wechatStatus} /><small>{loading ? t('taskCenter.loadingSchedule') : !enabled ? t('notifications.paused') : config?.configured ? t('taskCenter.configuredNotificationChannels', { count: activeChannels.length }) : t('notifications.emptyHint')}</small></span>
        <RightOutlined />
      </button>
    </>}
    {!compact && !summaryCard && <>
      <h3>{t('notifications.title')}</h3>
      {loading && <Spin size="small" />}
      {errorView}
      {config && <div className="notification-surface">
        <div className="notification-row"><ChannelBrand channel="desktop" /><div><strong>{t('notifications.' + (config.configured ? 'configured' : 'unconfigured'))}</strong><p>{t('notifications.' + (config.configured ? 'snapshotHint' : 'emptyHint'))}</p></div></div>
        {config.config && channels.filter(c => (c === 'desktop' && config.config?.channels.desktop) || config.config?.channels[c]?.enabled || config.config?.channels[c]?.account_id).map(c => <ConfiguredChannel key={c} channel={c} rule={config.config!.channels[c]!} blocked={Boolean(config.config?.channels[c]?.enabled && blocked(c))} unavailable={c !== 'wechat' && config.availability[c]?.state === 'unavailable'} wechatStatus={c === 'wechat' ? wechatStatus : undefined} liveAccount={c === 'wechat' ? wechat.account : undefined} />)}
      </div>}
      {!enabled && <Alert type="warning" message={t('notifications.paused')} />}
    </>}
    {!summaryCard && <Button icon={<SettingOutlined aria-hidden="true" />} disabled={loading || (draftMode && !config)} onClick={() => { setDraft(config?.config); setOpen(true); }}>{t('notifications.configure')}</Button>}
    {wechatWarning}
    {compact && !open && errorView}
    {compact && config && <NotificationSummary compact value={config.config} preferences={preferences} />}
    {!compact && <>
      {!summaryCard && config?.config && <NotificationSummary value={config.config} preferences={preferences} />}
      {showHistory && <><h3>{t('notifications.history')}</h3>
      <Select aria-label={t('notifications.chooseRun')} style={{ width: '100%' }} placeholder={t('notifications.chooseRun')} value={taskId} onChange={setTaskId} options={runs.map(run => ({ value: run.id, label: `${new Date(run.created_at).toLocaleString()} · ${run.title || run.id}` }))} />
      {runs.length < runTotal && <Button onClick={() => setRunPage(n => n + 1)}>{t('notifications.loadMore')}</Button>}
      {taskId ? <NotificationHistory key={taskId} taskId={taskId} /> : <p>{t('notifications.noHistory')}</p>}</>}
    </>}
    </>}
    <Drawer className="notification-editor-drawer" zIndex={1200} width={680} open={open} title={<><div>{t('notifications.configure')}</div><small>{title} · {t('notifications.onlyThisTask')}</small></>} onClose={cancelEditor} footer={<div className="notification-footer"><Button className="notification-restore-defaults" type="link" disabled={loading || saving || conflict || preferencesBusy} onClick={restoreGlobalDefaults}>{t('notifications.restoreGlobalDefaults')}</Button><div className="notification-footer-actions"><Button disabled={saving || preferencesBusy} onClick={cancelEditor}>{t('notifications.cancel')}</Button><Button type="primary" loading={saving} disabled={draft === undefined || loading || conflict || preferencesBusy || !preferences} onClick={() => void save()}>{t('notifications.save')}</Button></div></div>}>
      {errorView}
      {loading ? <Spin /> : draft !== undefined && <><Alert className="notification-task-info" type={enabled ? 'info' : 'warning'} showIcon message={t('notifications.' + (enabled ? 'draftSaveHint' : 'paused'))} action={!enabled && <Button onClick={() => openSettings()}>{t('notifications.openSettings')}</Button>} /><div className="notification-task-overview"><span className="notification-task-overview-icon"><BellOutlined /></span><div className="notification-grow"><h3>{t('notifications.taskOverview')}</h3><NotificationSummary value={draft} preferences={preferences} /></div><Tag className="notification-task-channel-count" color="blue">{t('notifications.configuredChannelCount', { count: channels.filter(channel => draft?.channels[channel]?.enabled).length })}</Tag></div><RuleEditor variant="task" value={draft || emptyRule()} onChange={next => void change(next)} preferences={preferences} onOpenSettings={openSettings} accountsRefresh={accountsRefresh} disabled={saving || conflict || preferencesBusy || !preferences} /></>}
      {open && selectedTaskId && <section><h3>{t('taskCenter.currentExecutionNotifications')}</h3><NotificationHistory key={selectedTaskId} taskId={selectedTaskId} /></section>}
    </Drawer>
    <Modal zIndex={1300} width={1000} open={settingsOpen} title={t('notifications.title')} maskClosable={!settingsBusy} keyboard={!settingsBusy} footer={<Button disabled={settingsBusy} onClick={closeSettings}>{t('notifications.return')}</Button>} onCancel={closeSettings}>{settingsOpen && <NotificationSettings requestedChannel={requestedChannel} onChannelEnabled={channelEnabled} onBusyChange={setSettingsBusy} connectionInPlace />}</Modal>
  </div>;
}
