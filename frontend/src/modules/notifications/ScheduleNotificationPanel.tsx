import { SettingOutlined } from '@ant-design/icons';
import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Drawer, Modal, Select, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { listScheduleTasks, type Task } from '@/modules/taskCenter/api';
import RuleEditor from './RuleEditor';
import NotificationSummary from './NotificationSummary';
import NotificationSettings from './NotificationSettings';
import NotificationHistory from './NotificationHistory';
import ChannelBrand from './ChannelBrand';
import { channels, events, getExecutionNotifications, emptyRule, getAccountDetail, getPreferences, getScheduleNotifications, notificationError, putScheduleNotifications, ruleError, type ChannelName, type ChannelRule, type AccountDetail, type NotificationConfig, type ScheduleNotifications, type NotificationUpdate } from './api';

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

export default function ScheduleNotificationPanel({ scheduleId, compact = false, draftMode = false, title, editorOpen, onEditorOpenChange, onDraftChange }: {
  scheduleId?: string; compact?: boolean; draftMode?: boolean; title?: string;
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
  const [conflict, setConflict] = useState(false);
  const [runs, setRuns] = useState<Task[]>([]);
  const [runPage, setRunPage] = useState(1);
  const [runTotal, setRunTotal] = useState(0);
  const [taskId, setTaskId] = useState<string>();
  const load = useCallback(async () => {
    setLoading(true); setError('');
    try {
      const [c, p] = await Promise.all([scheduleId ? getScheduleNotifications(scheduleId) : Promise.resolve(undefined), getPreferences()]);
      const current = c || { revision: 0, configured: true, config: p.defaults, availability: {} };
      setConfig(current); setDraft(current.config); setEnabled(p.enabled); setConflict(false);
    } catch { setError('loadFailed'); } finally { setLoading(false); }
  }, [scheduleId]);
  useEffect(() => { if (!compact || draftMode) void load(); }, [load, compact, draftMode]);
  useEffect(() => { if (compact && !draftMode && open) void load(); }, [load, compact, draftMode, open]);
  useEffect(() => {
    if (compact || !scheduleId) return;
    let active = true;
    listScheduleTasks(scheduleId, runPage).then(page => { if (active) { setRuns(old => runPage === 1 ? page.items : [...old, ...page.items]); setRunTotal(page.total); if (runPage === 1) setTaskId(page.items[0]?.id); } }).catch(() => { if (active) setError('loadFailed'); });
    return () => { active = false; };
  }, [scheduleId, compact, runPage]);
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
    catch (e) { const detail = notificationError(e); setError(detail.reason === 'NOTIFICATION_CONFIG_CONFLICT' ? 'conflict' : detail.reason); setConflict(detail.reason === 'NOTIFICATION_CONFIG_CONFLICT'); }
    finally { setSaving(false); }
  };
  const change = async (next: NotificationConfig | null, apply = () => setDraft(next)) => {
    const turningOff = draft && (events.some(e => draft.events[e].enabled && !next?.events[e].enabled) || channels.some(c => draft.channels[c]?.enabled && !next?.channels[c]?.enabled));
    if (!turningOff || !scheduleId) { apply(); return; }
    setLoading(true);
    try {
      const affected: Task[] = [];
      for (let page = 1; page <= 100; page += 1) {
        const result = await listScheduleTasks(scheduleId, page, 100);
        const active = result.items.filter(task => ['pending', 'running', 'waiting', 'waiting_inputs'].includes(task.status));
        for (const task of active) {
          const { snapshot } = await getExecutionNotifications(task.id);
          const rule = snapshot.config;
          if (rule && (events.some(e => rule.events[e].enabled && draft?.events[e].enabled && !next?.events[e].enabled) || channels.some(c => rule.channels[c]?.enabled && draft?.channels[c]?.enabled && !next?.channels[c]?.enabled))) affected.push(task);
        }
        if (page * 100 >= result.total) break;
        if (page === 100) throw new Error('TOO_MANY_RUNS');
      }
      if (affected.length) Modal.confirm({ zIndex: 1600, title: t('notifications.confirmOff'), content: <><p>{t('notifications.snapshotHint')}</p>{affected.map(task => <p key={task.id}>{task.title || task.id}</p>)}</>, okText: t('notifications.confirm'), cancelText: t('notifications.cancel'), onOk: apply });
      else apply();
    } catch { setError('loadFailed'); } finally { setLoading(false); }
  };
  const cancelEditor = () => { if (!saving) { setDraft(config?.config); setError(''); setOpen(false); } };
  const closeSettings = () => { setSettingsOpen(false); void getPreferences().then(p => setEnabled(p.enabled)).catch(() => setError('loadFailed')); };
  const errorView = error && <Alert type="error" message={t('notifications.' + error, { defaultValue: t('notifications.saveFailed') })} action={<Button onClick={() => void load()}>{t('notifications.reload')}</Button>} />;
  return <div className={compact ? 'notification-compact' : 'notification-schedule'}>
    {!compact && <>
      <h3>{t('notifications.title')}</h3>
      {loading && <Spin size="small" />}
      {errorView}
      {config && <div className="notification-surface">
        <div className="notification-row"><ChannelBrand channel="desktop" /><div><strong>{t('notifications.' + (config.configured ? 'configured' : 'unconfigured'))}</strong><p>{t('notifications.' + (config.configured ? 'snapshotHint' : 'emptyHint'))}</p></div></div>
        {config.config && channels.filter(c => (c === 'desktop' && config.config?.channels.desktop) || config.config?.channels[c]?.enabled || config.config?.channels[c]?.account_id).map(c => <ConfiguredChannel key={c} channel={c} rule={config.config!.channels[c]!} unavailable={config.availability[c]?.state === 'unavailable'} />)}
      </div>}
      {!enabled && <Alert type="warning" message={t('notifications.paused')} />}
    </>}
    <Button icon={<SettingOutlined aria-hidden="true" />} disabled={loading || (draftMode && !config)} onClick={() => { setDraft(config?.config); setOpen(true); }}>{t('notifications.configure')}</Button>
    {compact && !open && errorView}
    {compact && config && <NotificationSummary compact value={config.config} />}
    {!compact && <>
      {config?.config && <NotificationSummary value={config.config} />}
      {config?.configured && <Button disabled={saving || loading} onClick={() => void change(null, () => { void save(null); })}>{t('notifications.clear')}</Button>}
      <h3>{t('notifications.history')}</h3>
      <Select aria-label={t('notifications.chooseRun')} style={{ width: '100%' }} placeholder={t('notifications.chooseRun')} value={taskId} onChange={setTaskId} options={runs.map(run => ({ value: run.id, label: `${new Date(run.created_at).toLocaleString()} · ${run.title || run.id}` }))} />
      {runs.length < runTotal && <Button onClick={() => setRunPage(n => n + 1)}>{t('notifications.loadMore')}</Button>}
      {taskId ? <NotificationHistory key={taskId} taskId={taskId} /> : <p>{t('notifications.noHistory')}</p>}
    </>}
    <Drawer className="notification-editor-drawer" zIndex={1200} width={640} open={open} title={<><div>{t('notifications.configure')}</div><small>{title} · {t('notifications.onlyThisTask')}</small></>} onClose={cancelEditor} footer={<div className="notification-footer"><Button disabled={loading || saving || conflict} onClick={async () => { setLoading(true); try { const p = await getPreferences(); setDraft(p.defaults); setEnabled(p.enabled); } catch { setError('loadFailed'); } finally { setLoading(false); } }}>{t('notifications.restore')}</Button><div className="notification-footer-actions"><Button disabled={saving} onClick={cancelEditor}>{t('notifications.cancel')}</Button><Button type="primary" loading={saving} disabled={draft === undefined || loading || conflict} onClick={() => void save()}>{t('notifications.save')}</Button></div></div>}>
      {errorView}
      {loading ? <Spin /> : draft !== undefined && <><Alert type={enabled ? 'info' : 'warning'} message={t('notifications.' + (enabled ? 'draftSaveHint' : 'paused'))} action={!enabled && <Button onClick={() => setSettingsOpen(true)}>{t('notifications.openSettings')}</Button>} /><div className="notification-task-overview"><ChannelBrand channel="desktop" /><div className="notification-grow"><h3>{t('notifications.taskOverview')}</h3><NotificationSummary value={draft} /></div><Tag color="blue">{t('notifications.onlyThisTask')}</Tag></div><RuleEditor variant="task" value={draft || emptyRule()} onChange={next => void change(next)} disabled={saving || conflict} /><Button disabled={!draft || saving} onClick={() => void change(null)}>{t('notifications.clear')}</Button></>}
    </Drawer>
    <Modal zIndex={1300} width={1000} open={settingsOpen} title={t('notifications.title')} footer={<Button onClick={closeSettings}>{t('notifications.return')}</Button>} onCancel={closeSettings}>{settingsOpen && <NotificationSettings />}</Modal>
  </div>;
}
