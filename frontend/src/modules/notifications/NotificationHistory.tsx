import { Alert, Button, Spin, Tag } from 'antd';
import ChannelBrand from './ChannelBrand';
import { useNotificationHistory } from './useNotificationHistory';

export default function NotificationHistory({ taskId }: { taskId: string }) {
  const { t, execution, rows, cursor, loading, error, busy, snapshotChannels, refresh, loadMore } = useNotificationHistory(taskId);
  return <div className="notification-history">
    <p>{t('notifications.historyHint')}</p>
    <Button disabled={loading || Boolean(busy)} onClick={refresh}>{t('notifications.refresh')}</Button>
    {error && <Alert type="error" message={t('notifications.' + error, { defaultValue: t('notifications.loadFailed') })} />}
    {loading && <Spin size="small" />}
    {execution && <>
      <p>{t('notifications.snapshot')} · {execution.snapshot.revision} · {snapshotChannels}</p>
      {rows.map(row => <div className="notification-history-row" key={row.id}>
        <ChannelBrand channel={row.channel} />
        <div className="notification-grow">
          <strong>{t('notifications.' + row.channel)} · {row.recipient}</strong>
          <p>{row.createdAt} · {t('notifications.' + row.content)}</p>
          {row.reason && <small>{t('notifications.' + row.reason, { defaultValue: t('notifications.unavailable') })}</small>}
          {row.retryOf && <small>{t('notifications.retryOf')} · {row.retryOf}</small>}
        </div>
        <Tag>{t('notifications.' + row.status, { defaultValue: t('notifications.status') })}</Tag>
        {row.retryable && <Button aria-label={t('notifications.retry')} disabled={row.retryDisabled} loading={row.retryBusy} onClick={row.onRetry}>{t('notifications.retry')}</Button>}
      </div>)}
      {!rows.length && <p>{t('notifications.noHistory')}</p>}
    </>}
    {cursor && <Button disabled={loading} onClick={loadMore}>{t('notifications.loadMore')}</Button>}
  </div>;
}
