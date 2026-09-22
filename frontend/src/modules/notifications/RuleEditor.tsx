import { useEffect, useState } from 'react';
import { Alert, Button, Select, Spin, Switch, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { channelAccountLabel, isChannelAccountAvailable, isChannelAccountPendingActivation, listChannelAccounts, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';
import { ArrowRightOutlined, CheckCircleOutlined, ExclamationCircleOutlined, PauseCircleOutlined } from '@ant-design/icons';
import { isDesktopRuntime } from '@/runtime/mode';
import { channels, events, providers, type NotificationConfig } from './api';
import TargetPicker from './TargetPicker';
import ChannelBrand from './ChannelBrand';
import BrowserPermission from './BrowserPermission';
import { desktopNotificationsAuthorized } from './browser';
import './index.scss';

export default function RuleEditor({ value, onChange, disabled = false, variant = 'settings' }: {
  value: NotificationConfig; onChange: (config: NotificationConfig) => void; disabled?: boolean; variant?: 'settings' | 'task';
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [desktopAuthorized, setDesktopAuthorized] = useState(desktopNotificationsAuthorized);
  const [configuringChannel, setConfiguringChannel] = useState<ChannelProvider>();
  const openConnection = (provider: ChannelProvider = 'feishu') => navigate(`/settings?section=channels&provider=${provider}`);
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    Promise.all(providers.map(listChannelAccounts)).then(results => { if (active) setAccounts(results.flatMap(r => r.items)); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [refresh]);
  useEffect(() => {
    const update = () => setDesktopAuthorized(desktopNotificationsAuthorized());
    window.addEventListener('focus', update);
    window.addEventListener('lazymind:notification-permission-change', update);
    return () => {
      window.removeEventListener('focus', update);
      window.removeEventListener('lazymind:notification-permission-change', update);
    };
  }, []);
  const eventView = <div className="notification-events">
    <div className="notification-section-heading"><div><h3>{t('notifications.events')}</h3>{variant === 'settings' && <p>{t('notifications.eventsDefaultsHint')}</p>}</div>{variant === 'settings' && <Tag className="notification-new-task-tag" color="blue">{t('notifications.newTasksOnly')}</Tag>}</div>
    <section className="notification-surface">{events.map(event => <div className="notification-row" key={event}>
      <span className={`notification-event-icon is-${event}`}>{event === 'succeeded' ? <CheckCircleOutlined /> : event === 'failed' ? <ExclamationCircleOutlined /> : <PauseCircleOutlined />}</span><div className="notification-grow"><strong>{t('notifications.' + event)}</strong><p>{t('notifications.' + event + 'Hint')}</p></div>
      <Select aria-label={`${t('notifications.' + event)} · ${t('notifications.content')}`} disabled={disabled || !value.events[event].enabled} value={value.events[event].content} onChange={(content: 'summary' | 'full') => onChange({ ...value, events: { ...value.events, [event]: { ...value.events[event], content } } })} options={['summary', 'full'].map(v => ({ value: v, label: t('notifications.' + v) }))} />
      <Switch aria-label={t('notifications.' + event)} checked={value.events[event].enabled} disabled={disabled} onChange={(enabled: boolean) => onChange({ ...value, events: { ...value.events, [event]: { ...value.events[event], enabled } } })} />
    </div>)}</section>
    </div>;
  const channelView = <div className="notification-channels">
    <div className="notification-section-heading"><div><h3>{t('notifications.channels')}</h3><p>{t('notifications.' + (variant === 'settings' ? 'channelsHint' : 'taskChannelsHint'))}</p></div>{variant === 'settings' && <Button type="link" className="notification-link-action" onClick={() => openConnection()}>{t('notifications.connectTitle')}<ArrowRightOutlined /></Button>}</div>
    {loading && <Spin size="small" />}
    {error && <Alert type="error" message={t('notifications.loadFailed')} action={<Button onClick={() => setRefresh(n => n + 1)}>{t('notifications.retry')}</Button>} />}
    {variant === 'settings' && !loading && !error && !accounts.some(isChannelAccountAvailable) && <Alert type="warning" showIcon message={t('notifications.noExternalChannels')} action={<Button disabled={disabled} onClick={() => openConnection()}>{t('notifications.connectTitle')}</Button>} />}
    <section className="notification-surface">{channels.map(channel => {
      const rule = value.channels[channel];
      const account = accounts.find(a => a.id === rule?.account_id && a.provider === channel);
      const providerAccounts = accounts.filter(a => a.provider === channel);
      const availableAccounts = providerAccounts.filter(isChannelAccountAvailable);
      const hasAvailableAccount = availableAccounts.length > 0;
      const available = channel === 'desktop' ? desktopAuthorized : rule?.account_id ? Boolean(account && isChannelAccountAvailable(account)) : hasAvailableAccount;
      const canConfigure = channel === 'desktop' ? desktopAuthorized : hasAvailableAccount;
      const configuring = variant === 'settings' && channel !== 'desktop' && configuringChannel === channel;
      const pendingActivation = channel === 'wechat' && !available && (account ? isChannelAccountPendingActivation(account) : providerAccounts.some(isChannelAccountPendingActivation));
      return <div key={channel} className={`notification-channel-block is-${channel}`}><div className="notification-row notification-channel">
        <ChannelBrand channel={channel} avatar={account?.avatar_url} />
        <div className="notification-grow"><strong>{t('notifications.' + channel)}</strong> <Tag className={`notification-status is-${channel === 'desktop' ? desktopAuthorized ? 'connected' : 'disconnected' : pendingActivation ? 'pending' : available ? 'connected' : 'disconnected'}`}>{t('notifications.' + (channel === 'desktop' ? desktopAuthorized ? 'authorized' : 'notAuthorized' : pendingActivation ? 'pendingActivation' : available ? 'connected' : 'notConnected'))}</Tag>
          {channel === 'desktop' && !isDesktopRuntime() ? <BrowserPermission /> : <p>{channel === 'desktop' ? t('notifications.desktopHint') : variant === 'settings' ? t('notifications.' + channel + 'Hint') : rule?.account_id ? `${account ? channelAccountLabel(account) : t('notifications.accountUnavailable')} · ${rule.recipient_id || t('notifications.chooseRecipient')}` : t('notifications.' + channel + 'Hint')}</p>}
          {channel !== 'desktop' && rule?.account_id && !available && !loading && <small className="notification-warning">{t(pendingActivation ? 'notifications.pendingActivationHint' : 'notifications.unavailable')}</small>}
        </div>
        {channel !== 'desktop' && !canConfigure && <Button className="notification-configure-action" icon={<ArrowRightOutlined />} disabled={disabled || loading} onClick={() => openConnection(channel)}>{t('notifications.connect')}</Button>}
        {(canConfigure || rule?.enabled || rule?.account_id || configuring) && <Switch aria-label={t('notifications.' + channel)} checked={Boolean(rule?.enabled || configuring)} disabled={disabled || (loading && channel !== 'desktop') || (!canConfigure && !rule?.enabled)} onChange={(enabled: boolean) => {
          if (variant === 'settings' && channel !== 'desktop') {
            if (!enabled && configuring && !rule?.enabled) { setConfiguringChannel(undefined); return; }
            if (enabled && (!rule?.account_id || !rule?.recipient_id)) { setConfiguringChannel(channel); return; }
            if (configuringChannel === channel) setConfiguringChannel(undefined);
          }
          onChange({ ...value, channels: { ...value.channels, [channel]: { ...rule, enabled } } });
        }} />}
      </div>
      {channel !== 'desktop' && (rule?.enabled || configuring) && canConfigure && <TargetPicker key={channel} disabled={disabled} inline completeOnly={variant === 'settings'} provider={channel} accounts={availableAccounts} current={rule} onClose={() => {}} onSave={target => {
        if (configuringChannel === channel) setConfiguringChannel(undefined);
        onChange({ ...value, channels: { ...value.channels, [channel]: target } });
      }} />}
      </div>;
    })}</section></div>;
  return <div className={`notification-rules is-${variant}`}>
    {variant === 'settings' ? <>{channelView}{eventView}</> : <>{eventView}{channelView}</>}
  </div>;
}
