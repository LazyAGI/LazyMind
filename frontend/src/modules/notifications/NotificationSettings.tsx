import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';
import { BellOutlined } from '@ant-design/icons';
import RuleEditor from './RuleEditor';
import { getPreferences, notificationError, patchPreferences, ruleError, type NotificationConfig, type Preferences } from './api';

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
        Modal.confirm({ title: t('notifications.confirmGlobal'), content: <><p>{t('notifications.confirmGlobalHint')}</p>{detail.running_task_ids.map(id => <p key={id}>{id}</p>)}</>, okText: t('notifications.off'), cancelText: t('notifications.cancel'), onOk: () => save(patch, detail.running_task_ids, revision) });
      } else if (detail.reason === 'NOTIFICATION_CONFIG_CONFLICT') { setConflict(true); setError('conflict'); }
      else setError(detail.reason);
    } finally { saving.current = false; if (mounted.current) setBusy(false); }
  };
  const changeRule = (next: NotificationConfig) => {
    const problem = ruleError(next, true);
    if (problem) { setError(problem); return; }
    setDraft(next); void save({ defaults: next });
  };
  return <div className="notification-settings">
    <header className="notification-heading"><BellOutlined /><div><h2>{t('notifications.title')}</h2><p>{t('notifications.subtitle')}</p></div></header>
    {error && <Alert type="error" showIcon message={t('notifications.' + error, { defaultValue: t('notifications.saveFailed') })} action={<Button disabled={busy} onClick={() => { if (!conflict && pendingPatch.current) void save(pendingPatch.current); else void load(); }}>{t('notifications.' + (!conflict && pendingPatch.current ? 'retry' : 'reload'))}</Button>} />}
    {!prefs ? busy ? <Spin /> : null : <>
      <section className="notification-surface"><div className="notification-row"><div className="notification-grow"><strong>{t('notifications.global')}</strong><p>{t('notifications.globalHint')}</p></div><Switch aria-label={t('notifications.global')} checked={prefs.enabled} loading={busy} disabled={busy || conflict} onChange={(enabled: boolean) => void save({ enabled })} /></div></section>
      {!prefs.enabled && <Alert type="warning" showIcon message={t('notifications.paused')} />}
      <p className="notification-note">{t('notifications.defaultsHint')}</p>
      {draft && <RuleEditor value={draft} onChange={changeRule} disabled={busy || conflict} />}
    </>}
  </div>;
}
