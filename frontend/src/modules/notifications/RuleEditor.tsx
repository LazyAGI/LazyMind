import { useEffect, useState } from 'react';
import { Alert, Button, Select, Spin, Switch, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { channelAccountLabel, isChannelAccountAvailable, isChannelAccountPendingActivation, listChannelAccounts, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';
import { ArrowRightOutlined, CheckCircleOutlined, ExclamationCircleOutlined, PauseCircleOutlined } from '@ant-design/icons';
import { isDesktopRuntime } from '@/runtime/mode';
import { channels, events, providers, type ChannelRule, type NotificationConfig } from './api';
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
  const [picker, setPicker] = useState<ChannelProvider>();
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
      const available = channel === 'desktop' ? desktopAuthorized : rule?.account_id ? Boolean(account && isChannelAccountAvailable(account)) : providerAccounts.some(isChannelAccountAvailable);
      const pendingActivation = channel === 'wechat' && !available && (account ? isChannelAccountPendingActivation(account) : providerAccounts.some(isChannelAccountPendingActivation));
      return <div key={channel} className={`notification-channel-block is-${channel}`}><div className="notification-row notification-channel">
        <ChannelBrand channel={channel} avatar={account?.avatar_url} />
        <div className="notification-grow"><strong>{t('notifications.' + channel)}</strong> <Tag className={`notification-status is-${channel === 'desktop' ? desktopAuthorized ? 'connected' : 'disconnected' : pendingActivation ? 'pending' : available ? 'connected' : 'disconnected'}`}>{t('notifications.' + (channel === 'desktop' ? desktopAuthorized ? 'authorized' : 'notAuthorized' : pendingActivation ? 'pendingActivation' : available ? 'connected' : 'notConnected'))}</Tag>
          {channel === 'desktop' && !isDesktopRuntime() ? <BrowserPermission /> : <p>{channel === 'desktop' ? t('notifications.desktopHint') : variant === 'settings' ? t('notifications.' + channel + 'Hint') : rule?.account_id ? `${account ? channelAccountLabel(account) : t('notifications.accountUnavailable')} · ${rule.recipient_id || t('notifications.chooseRecipient')}` : t('notifications.' + channel + 'Hint')}</p>}
          {variant === 'task' && rule?.account_id && !available && !loading && <small className="notification-warning">{t(pendingActivation ? 'notifications.pendingActivationHint' : 'notifications.unavailable')}</small>}
        </div>
        {channel !== 'desktop' && variant === 'task' && !available && <Button className="notification-configure-action" icon={<ArrowRightOutlined />} disabled={disabled || loading} onClick={() => openConnection(channel)}>{t('notifications.connect')}</Button>}
        {channel !== 'desktop' && variant === 'settings' && !available && <Button className="notification-configure-action" icon={<ArrowRightOutlined />} disabled={disabled || loading} onClick={() => openConnection(channel)}>{t('notifications.connect')}</Button>}
        {(available || rule?.enabled || rule?.account_id) && <Switch aria-label={t('notifications.' + channel)} checked={Boolean(rule?.enabled)} disabled={disabled || (loading && channel !== 'desktop') || (!available && !rule?.enabled)} onChange={(enabled: boolean) => {
          if (variant === 'settings') { onChange({ ...value, channels: { ...value.channels, [channel]: { enabled } } }); return; }
          if (enabled && variant === 'task' && channel !== 'desktop') { onChange({ ...value, channels: { ...value.channels, [channel]: { ...rule, enabled } } }); return; }
          if (enabled && channel !== 'desktop' && (!rule?.account_id || !rule.recipient_id || !account || !isChannelAccountAvailable(account))) { setPicker(channel); return; }
          onChange({ ...value, channels: { ...value.channels, [channel]: { ...rule, enabled } } });
        }} />}
      </div>
      {variant === 'task' && channel !== 'desktop' && rule?.enabled && available && <TargetPicker key={channel} disabled={disabled} inline provider={channel} accounts={accounts.filter(a => a.provider === channel && isChannelAccountAvailable(a))} current={rule} onClose={() => {}} onSave={target => onChange({ ...value, channels: { ...value.channels, [channel]: target } })} />}
      </div>;
    })}</section></div>;
  return <div className={`notification-rules is-${variant}`}>
    {variant === 'settings' ? <>{channelView}{eventView}</> : <>{eventView}{channelView}</>}
    {picker && <TargetPicker key={picker} disabled={disabled} provider={picker} accounts={accounts.filter(a => a.provider === picker)} current={value.channels[picker]} onClose={() => setPicker(undefined)} onSave={target => { onChange({ ...value, channels: { ...value.channels, [picker]: target } }); setPicker(undefined); }} />}
  </div>;
}
