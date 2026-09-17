import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Drawer, Modal, Select, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { listScheduleTasks, type Task } from '@/modules/taskCenter/api';
import RuleEditor from './RuleEditor';
import NotificationHistory from './NotificationHistory';
import ChannelBrand from './ChannelBrand';
import { channels, emptyRule, getAccountDetail, getPreferences, getScheduleNotifications, notificationError, putScheduleNotifications, ruleError, type ChannelName, type ChannelRule, type AccountDetail, type NotificationConfig, type ScheduleNotifications } from './api';

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

export default function ScheduleNotificationPanel({ scheduleId, compact = false }: { scheduleId: string; compact?: boolean }) {
  const { t } = useTranslation();
  const [config, setConfig] = useState<ScheduleNotifications>();
  const [draft, setDraft] = useState<NotificationConfig>();
  const [enabled, setEnabled] = useState(true);
  const [open, setOpen] = useState(false);
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
      const [c, p] = await Promise.all([getScheduleNotifications(scheduleId), getPreferences()]);
      setConfig(c); setDraft(c.config || emptyRule()); setEnabled(p.enabled); setConflict(false);
    } catch { setError('loadFailed'); } finally { setLoading(false); }
  }, [scheduleId]);
  useEffect(() => { if (!compact || open) void load(); }, [compact, open, load]);
  useEffect(() => {
    if (compact) return;
    let active = true;
    listScheduleTasks(scheduleId, runPage).then(page => { if (active) { setRuns(old => runPage === 1 ? page.items : [...old, ...page.items]); setRunTotal(page.total); if (runPage === 1) setTaskId(page.items[0]?.id); } }).catch(() => { if (active) setError('loadFailed'); });
    return () => { active = false; };
  }, [scheduleId, compact, runPage]);
  const save = async () => {
    if (!config || !draft || saving) return;
    const reason = ruleError(draft);
    if (reason) { setError(reason); return; }
    setSaving(true); setError('');
    try { const result = await putScheduleNotifications(scheduleId, config.revision, draft); setConfig(result); setOpen(false); }
    catch (e) { const detail = notificationError(e); setError(detail.reason === 'NOTIFICATION_CONFIG_CONFLICT' ? 'conflict' : detail.reason); setConflict(detail.reason === 'NOTIFICATION_CONFIG_CONFLICT'); }
    finally { setSaving(false); }
  };
  const change = (next: NotificationConfig) => {
    const turningOff = draft && (Object.keys(draft.events).some(key => draft.events[key as keyof typeof draft.events].enabled && !next.events[key as keyof typeof next.events].enabled) || channels.some(c => draft.channels[c]?.enabled && !next.channels[c]?.enabled));
    if (turningOff) Modal.confirm({ title: t('notifications.confirmOff'), content: t('notifications.snapshotHint'), okText: t('notifications.off'), cancelText: t('notifications.cancel'), onOk: () => setDraft(next) });
    else setDraft(next);
  };
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
    <Button onClick={() => setOpen(true)}>{t('notifications.configure')}</Button>
    {!compact && <>
      <h3>{t('notifications.history')}</h3>
      <Select aria-label={t('notifications.chooseRun')} style={{ width: '100%' }} placeholder={t('notifications.chooseRun')} value={taskId} onChange={setTaskId} options={runs.map(run => ({ value: run.id, label: `${new Date(run.created_at).toLocaleString()} · ${run.title || run.id}` }))} />
      {runs.length < runTotal && <Button onClick={() => setRunPage(n => n + 1)}>{t('notifications.loadMore')}</Button>}
      {taskId ? <NotificationHistory key={taskId} taskId={taskId} /> : <p>{t('notifications.noHistory')}</p>}
    </>}
    <Drawer width={640} open={open} title={t('notifications.configure')} onClose={() => { if (!saving) setOpen(false); }} extra={<Button onClick={() => { if (!saving) setOpen(false); }}>{t('notifications.cancel')}</Button>} footer={<div className="notification-footer"><Button disabled={loading || saving || conflict} onClick={async () => { setLoading(true); try { const p = await getPreferences(); setDraft(p.defaults); setEnabled(p.enabled); } catch { setError('loadFailed'); } finally { setLoading(false); } }}>{t('notifications.restore')}</Button><Button type="primary" loading={saving} disabled={!draft || loading || conflict} onClick={() => void save()}>{t('notifications.save')}</Button></div>}>
      {errorView}
      {loading ? <Spin /> : draft && <><Alert type={enabled ? 'info' : 'warning'} message={t('notifications.' + (enabled ? 'snapshotHint' : 'paused'))} /><RuleEditor value={draft} onChange={change} disabled={saving || conflict} /></>}
    </Drawer>
  </div>;
}
