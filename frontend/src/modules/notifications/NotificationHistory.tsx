import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { v4 as uuidv4 } from 'uuid';
import { getAttempts, getExecutionNotifications, notificationError, retryNotice, type Attempt, type ExecutionNotifications, type Notice } from './api';
import ChannelBrand from './ChannelBrand';

export default function NotificationHistory({ taskId }: { taskId: string }) {
  const { t } = useTranslation();
  const [execution, setExecution] = useState<ExecutionNotifications>();
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const [cursor, setCursor] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState<string>();
  const [refresh, setRefresh] = useState(0);
  const keys = useRef(new Map<string, string>());
  const retrying = useRef(false);
  useEffect(() => {
    let active = true;
    setLoading(true); setError('');
    Promise.allSettled([getExecutionNotifications(taskId), getAttempts(taskId)]).then(([e, page]) => {
      if (!active) return;
      if (e.status === 'fulfilled') setExecution(e.value);
      if (page.status === 'fulfilled') { setAttempts(page.value.items); setCursor(page.value.next_cursor); }
      if (e.status === 'rejected' || page.status === 'rejected') setError('loadFailed');
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [taskId, refresh]);
  const retry = async (attempt: Attempt, confirmed = false) => {
    if (retrying.current) return;
    if (attempt.status === 'unknown' && !confirmed) { Modal.confirm({ zIndex: 1600, title: t('notifications.retry'), content: t('notifications.unknownConfirm'), onOk: () => retry(attempt, true), okText: t('notifications.retry'), cancelText: t('notifications.cancel') }); return; }
    retrying.current = true; setBusy(attempt.notification_id); setError('');
    const key = keys.current.get(attempt.notification_id) || uuidv4(); keys.current.set(attempt.notification_id, key);
    try {
      const created = await retryNotice(attempt.notification_id, key, confirmed);
      keys.current.delete(attempt.notification_id);
      setAttempts(old => old.some(a => a.notification_id === created.notification_id) ? old : [...old, created]);
    } catch (e) {
      const detail = notificationError(e);
      if (detail.reason === 'NOTIFICATION_CONFIRMATION_REQUIRED' && !confirmed) Modal.confirm({ zIndex: 1600, title: t('notifications.retry'), content: t('notifications.unknownConfirm'), onOk: () => retry(attempt, true), okText: t('notifications.retry'), cancelText: t('notifications.cancel') });
      else setError(detail.reason);
    } finally { retrying.current = false; setBusy(undefined); }
  };
  // Older outbox rows predate source_notification_id. Resolve only through
  // Core's persisted gateway ID or an explicit retry link, never payload guesses.
  const sourceId = (attempt: Attempt): string | undefined => {
    const visited = new Set<string>();
    let current: Attempt | undefined = attempt;
    while (current && !visited.has(current.notification_id)) {
      if (current.source_notification_id) return current.source_notification_id;
      visited.add(current.notification_id);
      const source = execution?.items.find(n => n.gateway_id === current?.notification_id);
      if (source) return source.notification_id;
      const parent: string = current.retry_of;
      current = attempts.find(a => a.notification_id === parent);
    }
    return undefined;
  };
  const row = (notice: Notice | Attempt['payload'], attempt?: Attempt) => <div className="notification-history-row" key={attempt?.notification_id || ('notification_id' in notice ? notice.notification_id : undefined)}>
    <ChannelBrand channel={notice.channel} /><div className="notification-grow"><strong>{t('notifications.' + notice.channel)} · {notice.recipient_id || t('notifications.desktop')}</strong><p>{new Date(attempt?.created_at || ('created_at' in notice ? notice.created_at : '')).toLocaleString()} · {t('notifications.' + notice.content)}</p>
      {(attempt?.reason || notice.reason) && <small>{t('notifications.' + (attempt?.reason || notice.reason), { defaultValue: t('notifications.unavailable') })}</small>}
      {attempt?.retry_of && <small>{t('notifications.retryOf')} · {attempt.retry_of}</small>}
    </div><Tag>{t('notifications.' + ((attempt?.status || ('status' in notice ? notice.status : '')) === 'failed' ? 'deliveryFailed' : (attempt?.status || ('status' in notice ? notice.status : ''))), { defaultValue: t('notifications.status') })}</Tag>
    {attempt?.retryable && <Button aria-label={t('notifications.retry')} disabled={Boolean(busy) || Boolean(cursor) || attempts.some(a => Boolean(sourceId(attempt)) && sourceId(a) === sourceId(attempt) && ['queued', 'sending', 'sent'].includes(a.status))} loading={busy === attempt.notification_id} onClick={() => void retry(attempt)}>{t('notifications.retry')}</Button>}
  </div>;
  return <div className="notification-history">
    <p>{t('notifications.historyHint')}</p><Button disabled={loading || Boolean(busy)} onClick={() => setRefresh(n => n + 1)}>{t('notifications.refresh')}</Button>
    {error && <Alert type="error" message={t('notifications.' + error, { defaultValue: t('notifications.loadFailed') })} />}
    {loading && <Spin size="small" />}
    {execution && <>
      <p>{t('notifications.snapshot')} · {execution.snapshot.revision} · {execution.snapshot.config ? Object.entries(execution.snapshot.config.channels).filter(([,c]) => c?.enabled).map(([c]) => t('notifications.' + c)).join('、') : t('notifications.unconfigured')}</p>
      {execution.items.filter(n => n.channel === 'desktop' || !attempts.some(a => sourceId(a) === n.notification_id)).map(n => row(n))}
      {attempts.map(a => row(a.payload, a))}
      {!execution.items.length && !attempts.length && <p>{t('notifications.noHistory')}</p>}
    </>}
    {cursor && <Button disabled={loading} onClick={async () => { setLoading(true); try { const page = await getAttempts(taskId, cursor); setAttempts(old => [...old, ...page.items]); setCursor(page.next_cursor); } catch { setError('loadFailed'); } finally { setLoading(false); } }}>{t('notifications.loadMore')}</Button>}
  </div>;
}
