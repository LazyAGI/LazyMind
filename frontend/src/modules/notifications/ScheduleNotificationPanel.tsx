import { BellOutlined, FileTextOutlined, RightOutlined, SafetyCertificateOutlined, SettingOutlined, WarningOutlined } from '@ant-design/icons';
import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Drawer, Modal, Select, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { listScheduleTasks, type Task } from '@/modules/taskCenter/api';
import { channelAccountLabel } from '@/modules/channelGateway/api';
import RuleEditor from './RuleEditor';
import NotificationSummary from './NotificationSummary';
import NotificationSettings from './NotificationSettings';
import NotificationHistory from './NotificationHistory';
import ChannelBrand from './ChannelBrand';
import { availabilityError, channels, events, emptyRule, getAccountDetail, getPreferences, getScheduleNotifications, notificationError, putScheduleNotifications, ruleError, type ChannelName, type ChannelRule, type AccountDetail, type NotificationConfig, type ScheduleNotifications, type NotificationUpdate } from './api';

function ConfiguredChannel({ channel, rule, unavailable }: { channel: ChannelName; rule: ChannelRule; unavailable: boolean }) {
  const { t } = useTranslation();
  const [account, setAccount] = useState<AccountDetail>();
  useEffect(() => {
    let active = true;
    setAccount(undefined);
    if (rule.account_id) void getAccountDetail(rule.account_id).then(value => { if (active) setAccount(value); }).catch(() => undefined);
    return () => { active = false; };
  }, [rule.account_id]);
  return <div className="notification-row"><ChannelBrand channel={channel} avatar={account?.avatar_url} /><div className="notification-grow"><strong>{t('notifications.' + channel)}</strong><p>{[account?.label || rule.account_id, rule.recipient_id].filter(Boolean).join(' · ')}</p></div><Tag>{t('notifications.' + (rule.enabled ? 'on' : 'off'))}</Tag>{unavailable && <Tag color="warning">{t('notifications.unavailable')}</Tag>}</div>;
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
  const [enabled, setEnabled] = useState(true);
  const [internalOpen, setInternalOpen] = useState(false);
  const open = editorOpen ?? internalOpen;
  const setOpen = (value: boolean) => { setInternalOpen(value); onEditorOpenChange?.(value); };
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [errorAccount, setErrorAccount] = useState('');
  const [conflict, setConflict] = useState(false);
  const [runs, setRuns] = useState<Task[]>([]);
  const [runPage, setRunPage] = useState(1);
  const [runTotal, setRunTotal] = useState(0);
  const [taskId, setTaskId] = useState<string>();
  const load = useCallback(async () => {
    setLoading(true); setError(''); setErrorAccount('');
    try {
      const [c, p] = await Promise.all([scheduleId ? getScheduleNotifications(scheduleId) : Promise.resolve(undefined), getPreferences()]);
      const current = c || { revision: 0, configured: true, config: p.defaults, availability: {} };
      const issue = availabilityError(current);
      setConfig(current); setDraft(current.config); setEnabled(p.enabled); setConflict(false); setError(issue?.reason || '');
      if (issue?.account_id) {
        void getAccountDetail(issue.account_id).then(account => setErrorAccount(channelAccountLabel(account))).catch(() => setErrorAccount(issue.provider));
      }
    } catch { setError('loadFailed'); } finally { setLoading(false); }
  }, [scheduleId]);
  useEffect(() => { if (!compact || draftMode) void load(); }, [load, compact, draftMode]);
  useEffect(() => { if (compact && !draftMode && open) void load(); }, [load, compact, draftMode, open]);
  useEffect(() => {
    if (compact || !scheduleId || !showHistory) return;
    let active = true;
    listScheduleTasks(scheduleId, runPage).then(page => { if (active) { setRuns(old => runPage === 1 ? page.items : [...old, ...page.items]); setRunTotal(page.total); if (runPage === 1) setTaskId(page.items[0]?.id); } }).catch(() => { if (active) setError('loadFailed'); });
    return () => { active = false; };
  }, [scheduleId, compact, runPage, showHistory]);
  const save = async (value = draft) => {
    if (!config || value === undefined || saving) return;
    const reason = value ? ruleError(value) : undefined;
    if (reason) { setError(reason); return; }
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
      if (accountId) void getAccountDetail(accountId).then(account => setErrorAccount(channelAccountLabel(account))).catch(() => setErrorAccount(provider));
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
  const cancelEditor = () => { if (!saving) { setDraft(config?.config); setError(''); setOpen(false); } };
  const closeSettings = () => { setSettingsOpen(false); void getPreferences().then(p => setEnabled(p.enabled)).catch(() => setError('loadFailed')); };
  const errorView = error && <Alert type="error" message={t('notifications.' + error, { account: errorAccount || t('notifications.account'), defaultValue: t('notifications.saveFailed') })} action={<Button onClick={() => void load()}>{t('notifications.reload')}</Button>} />;
  const activeChannels = channels.filter(channel => config?.config?.channels[channel]?.enabled);
  const summary = config?.configured
    ? activeChannels.length ? `${activeChannels.map(channel => t('notifications.' + channel)).join('、')} · ${events.filter(event => config.config?.events[event].enabled).map(event => t(event === 'succeeded' ? 'taskCenter.statusSuccess' : event === 'failed' ? 'taskCenter.statusFailed' : 'notifications.waiting')).join('、')}` : t('notifications.off')
    : t('notifications.unconfigured');
  return <div className={compact ? 'notification-compact' : 'notification-schedule'}>
    {!editorOnly && <>
    {summaryCard && <>
      <h3>{t('notifications.reminder')}</h3>
      {errorView}
      <button type='button' className='task-notification-entry' aria-label={t('notifications.configure')} disabled={loading || !config} onClick={() => { setDraft(config?.config); setOpen(true); }}>
        <span className='task-notification-entry-icon'><BellOutlined /></span>
        <span className='task-notification-entry-copy'><strong>{summary}</strong><small>{loading ? t('taskCenter.loadingSchedule') : !enabled ? t('notifications.paused') : config?.configured ? t('taskCenter.configuredNotificationChannels', { count: activeChannels.length }) : t('notifications.emptyHint')}</small></span>
        <RightOutlined />
      </button>
    </>}
    {!compact && !summaryCard && <>
      <h3>{t('notifications.title')}</h3>
      {loading && <Spin size="small" />}
      {errorView}
      {config && <div className="notification-surface">
        <div className="notification-row"><ChannelBrand channel="desktop" /><div><strong>{t('notifications.' + (config.configured ? 'configured' : 'unconfigured'))}</strong><p>{t('notifications.' + (config.configured ? 'snapshotHint' : 'emptyHint'))}</p></div></div>
        {config.config && channels.filter(c => (c === 'desktop' && config.config?.channels.desktop) || config.config?.channels[c]?.enabled || config.config?.channels[c]?.account_id).map(c => <ConfiguredChannel key={c} channel={c} rule={config.config!.channels[c]!} unavailable={config.availability[c]?.state === 'unavailable'} />)}
      </div>}
      {!enabled && <Alert type="warning" message={t('notifications.paused')} />}
    </>}
    {!summaryCard && <Button icon={<SettingOutlined aria-hidden="true" />} disabled={loading || (draftMode && !config)} onClick={() => { setDraft(config?.config); setOpen(true); }}>{t('notifications.configure')}</Button>}
    {compact && !open && errorView}
    {compact && config && <NotificationSummary compact value={config.config} />}
    {!compact && <>
      {!summaryCard && config?.config && <NotificationSummary value={config.config} />}
      {showHistory && <><h3>{t('notifications.history')}</h3>
      <Select aria-label={t('notifications.chooseRun')} style={{ width: '100%' }} placeholder={t('notifications.chooseRun')} value={taskId} onChange={setTaskId} options={runs.map(run => ({ value: run.id, label: `${new Date(run.created_at).toLocaleString()} · ${run.title || run.id}` }))} />
      {runs.length < runTotal && <Button onClick={() => setRunPage(n => n + 1)}>{t('notifications.loadMore')}</Button>}
      {taskId ? <NotificationHistory key={taskId} taskId={taskId} /> : <p>{t('notifications.noHistory')}</p>}</>}
    </>}
    </>}
    <Drawer className="notification-editor-drawer" zIndex={1200} width={680} open={open} title={<><div>{t('notifications.configure')}</div><small>{title} · {t('notifications.onlyThisTask')}</small></>} onClose={cancelEditor} footer={<div className="notification-footer"><Button className="notification-restore-defaults" type="link" disabled={loading || saving || conflict} onClick={async () => { setLoading(true); try { const p = await getPreferences(); setDraft(p.defaults); setEnabled(p.enabled); } catch { setError('loadFailed'); } finally { setLoading(false); } }}>{t('notifications.restoreGlobalDefaults')}</Button><div className="notification-footer-actions"><Button disabled={saving} onClick={cancelEditor}>{t('notifications.cancel')}</Button><Button type="primary" loading={saving} disabled={draft === undefined || loading || conflict} onClick={() => void save()}>{t('notifications.save')}</Button></div></div>}>
      {errorView}
      {loading ? <Spin /> : draft !== undefined && <><Alert className="notification-task-info" type={enabled ? 'info' : 'warning'} showIcon message={t('notifications.' + (enabled ? 'draftSaveHint' : 'paused'))} action={!enabled && <Button onClick={() => setSettingsOpen(true)}>{t('notifications.openSettings')}</Button>} /><div className="notification-task-overview"><span className="notification-task-overview-icon"><BellOutlined /></span><div className="notification-grow"><h3>{t('notifications.taskOverview')}</h3><NotificationSummary value={draft} /></div><Tag className="notification-task-channel-count" color="blue">{t('notifications.configuredChannelCount', { count: channels.filter(channel => draft?.channels[channel]?.enabled).length })}</Tag></div><RuleEditor variant="task" value={draft || emptyRule()} onChange={next => void change(next)} disabled={saving || conflict} /></>}
      {open && selectedTaskId && <section><h3>{t('taskCenter.currentExecutionNotifications')}</h3><NotificationHistory key={selectedTaskId} taskId={selectedTaskId} /></section>}
    </Drawer>
    <Modal zIndex={1300} width={1000} open={settingsOpen} title={t('notifications.title')} footer={<Button onClick={closeSettings}>{t('notifications.return')}</Button>} onCancel={closeSettings}>{settingsOpen && <NotificationSettings />}</Modal>
  </div>;
}
