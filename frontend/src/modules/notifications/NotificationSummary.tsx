import { useTranslation } from 'react-i18next';
import { channels, events, type NotificationConfig } from './api';

export default function NotificationSummary({ value, compact = false }: { value: NotificationConfig | null; compact?: boolean }) {
  const { t } = useTranslation();
  const configured = channels.filter(c => value?.channels[c]);
  if (!value || !configured.length) return <p>{t('notifications.emptyHint')}</p>;
  if (compact) return <p className="notification-summary-compact">{configured.filter(c => value.channels[c]?.enabled).map(c => t('notifications.' + c)).join('、') || t('notifications.off')} · {events.filter(e => value.events[e].enabled).map(e => t('notifications.' + e)).join('、')}</p>;
  return <div className="notification-summary">
    <p>{t('notifications.channelCount', { enabled: configured.filter(c => value.channels[c]?.enabled).length, count: configured.length })} · {configured.map(c => t('notifications.' + c)).join(' / ')}</p>
    <p>{events.filter(e => value.events[e].enabled).map(e => `${t('notifications.' + e)} · ${t('notifications.' + value.events[e].content)}`).join(' / ') || t('notifications.off')}</p>
  </div>;
}
