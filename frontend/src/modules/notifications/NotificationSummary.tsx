import { useTranslation } from 'react-i18next';
import { channelGateEnabled, channels, events, type ChannelName, type NotificationConfig, type Preferences } from './api';

export default function NotificationSummary({ value, compact = false, preferences }: { value: NotificationConfig | null; compact?: boolean; preferences?: Preferences }) {
  const { t } = useTranslation();
  const configured = channels.filter(c => value?.channels[c]);
  if (!value || !configured.length) return <p>{t('notifications.emptyHint')}</p>;
  const label = (channel: ChannelName) => `${t('notifications.' + channel)}${value.channels[channel]?.enabled && preferences && !channelGateEnabled(preferences, channel) ? `（${t('notifications.channelPaused')}）` : ''}`;
  if (compact) return <p className="notification-summary-compact">{configured.filter(c => value.channels[c]?.enabled).map(label).join('、') || t('notifications.off')} · {events.filter(e => value.events[e].enabled).map(e => t('notifications.' + e)).join('、')}</p>;
  return <div className="notification-summary">
    <p>{t('notifications.channelCount', { enabled: configured.filter(c => value.channels[c]?.enabled).length, count: configured.length })} · {configured.map(label).join(' / ')}</p>
    <p>{events.filter(e => value.events[e].enabled).map(e => `${t('notifications.' + e)} · ${t('notifications.' + value.events[e].content)}`).join(' / ') || t('notifications.off')}</p>
  </div>;
}
