import { useEffect, useRef, useState } from 'react';
import { Modal } from 'antd';
import { useTranslation } from 'react-i18next';
import { v4 as uuidv4 } from 'uuid';
import { getAttempts, getExecutionNotifications, notificationError, retryNotice, type Attempt, type ExecutionNotifications, type Notice } from './api';

export function useNotificationHistory(taskId: string) {
  const { t } = useTranslation();
  const [execution, setExecution] = useState<ExecutionNotifications>();
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const [cursor, setCursor] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState<string>();
  const [refreshVersion, setRefresh] = useState(0);
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
  }, [taskId, refreshVersion]);
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
  const loadMore = async () => {
    if (loading || !cursor) return;
    setLoading(true);
    try {
      const page = await getAttempts(taskId, cursor);
      setAttempts(old => [...old, ...page.items]);
      setCursor(page.next_cursor);
    } catch { setError('loadFailed'); }
    finally { setLoading(false); }
  };
  const makeRow = (notice: Notice | Attempt['payload'], attempt?: Attempt) => ({
    id: attempt?.notification_id || ('notification_id' in notice ? notice.notification_id : ''),
    channel: notice.channel,
    recipient: notice.recipient_id || t('notifications.desktop'),
    createdAt: new Date(attempt?.created_at || ('created_at' in notice ? notice.created_at : '')).toLocaleString(),
    content: notice.content,
    reason: attempt?.reason || notice.reason,
    retryOf: attempt?.retry_of,
    status: (attempt?.status || ('status' in notice ? notice.status : '')) === 'failed' ? 'deliveryFailed' : (attempt?.status || ('status' in notice ? notice.status : '')),
    retryable: attempt?.retryable,
    retryDisabled: Boolean(busy) || Boolean(cursor) || Boolean(attempt && attempts.some(a => Boolean(sourceId(attempt)) && sourceId(a) === sourceId(attempt) && ['queued', 'sending', 'sent'].includes(a.status))),
    retryBusy: busy === attempt?.notification_id && Boolean(busy),
    onRetry: () => { if (attempt) void retry(attempt); },
  });
  const rows = [
    ...(execution?.items.filter(n => n.channel === 'desktop' || !attempts.some(a => sourceId(a) === n.notification_id)).map(n => makeRow(n)) || []),
    ...attempts.map(a => makeRow(a.payload, a)),
  ];
  const snapshotChannels = execution?.snapshot.config
    ? Object.entries(execution.snapshot.config.channels).filter(([, channel]) => channel?.enabled).map(([channel]) => t('notifications.' + channel)).join('、')
    : t('notifications.unconfigured');
  return { t, execution, rows, cursor, loading, error, busy, snapshotChannels, retry, loadMore, refresh: () => setRefresh(n => n + 1) };
}
