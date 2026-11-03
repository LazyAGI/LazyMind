import { useTranslation } from 'react-i18next';
import { channelGateEnabled, channels, events, type ChannelName, type NotificationConfig, type Preferences } from './api';

export default function NotificationSummary({ value, compact = false, preferences }: { value: NotificationConfig | null; compact?: boolean; preferences?: Preferences }) {
  const { t } = useTranslation();
  const configured = channels.filter(c => value?.channels[c]);
  if (!value || !configured.length) return <p>{t('notifications.emptyHint')}</p>;
  const selected = configured.filter(channel => value.channels[channel]?.enabled);
  // Saved task choices survive a global pause. Only currently open gates may
  // be counted as enabled; unavailable preferences cannot establish that state.
  const countLabel = preferences
    ? t('notifications.channelCount', { enabled: selected.filter(channel => channelGateEnabled(preferences, channel)).length, count: configured.length })
    : t('notifications.configuredChannelCount', { count: selected.length });
  const label = (channel: ChannelName) => `${t('notifications.' + channel)}${value.channels[channel]?.enabled && preferences && !channelGateEnabled(preferences, channel) ? `（${t('notifications.channelPaused')}）` : ''}`;
  if (compact) return <p className="notification-summary-compact">{selected.map(label).join('、') || t('notifications.off')} · {events.filter(e => value.events[e].enabled).map(e => t('notifications.' + e)).join('、')}</p>;
  return <div className="notification-summary">
    <p>{countLabel} · {configured.map(label).join(' / ')}</p>
    <p>{events.filter(e => value.events[e].enabled).map(e => `${t('notifications.' + e)} · ${t('notifications.' + value.events[e].content)}`).join(' / ') || t('notifications.off')}</p>
  </div>;
}
