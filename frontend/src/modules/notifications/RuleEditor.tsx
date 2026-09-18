import { useEffect, useState } from 'react';
import { Alert, Button, Modal, Select, Spin, Switch, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { channelAccountLabel, listChannelAccounts, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';
import { TerminalConnectionPage } from '@/modules/channelGateway';
import { CheckCircleOutlined, ExclamationCircleOutlined, PauseCircleOutlined } from '@ant-design/icons';
import { isDesktopRuntime } from '@/runtime/mode';
import { channels, events, providers, type ChannelRule, type NotificationConfig } from './api';
import TargetPicker from './TargetPicker';
import ChannelBrand from './ChannelBrand';
import BrowserPermission from './BrowserPermission';
import { browserNotificationsSupported } from './browser';
import './index.scss';

export default function RuleEditor({ value, onChange, disabled = false, variant = 'settings' }: {
  value: NotificationConfig; onChange: (config: NotificationConfig) => void; disabled?: boolean; variant?: 'settings' | 'task';
}) {
  const { t } = useTranslation();
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [picker, setPicker] = useState<ChannelProvider>();
  const [pickerAccount, setPickerAccount] = useState<ChannelRule>();
  const [connecting, setConnecting] = useState<ChannelProvider>();
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    Promise.all(providers.map(listChannelAccounts)).then(results => { if (active) setAccounts(results.flatMap(r => r.items)); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [refresh]);
  const eventView = <div className="notification-events">
    <h3>{t('notifications.events')}</h3>
    <section className="notification-surface">{events.map(event => <div className="notification-row" key={event}>
      <span className={`notification-event-icon is-${event}`}>{event === 'succeeded' ? <CheckCircleOutlined /> : event === 'failed' ? <ExclamationCircleOutlined /> : <PauseCircleOutlined />}</span><div className="notification-grow"><strong>{t('notifications.' + event)}</strong><p>{t('notifications.' + event + 'Hint')}</p></div>
      <Select aria-label={`${t('notifications.' + event)} · ${t('notifications.content')}`} disabled={disabled || !value.events[event].enabled} value={value.events[event].content} onChange={(content: 'summary' | 'full') => onChange({ ...value, events: { ...value.events, [event]: { ...value.events[event], content } } })} options={['summary', 'full'].map(v => ({ value: v, label: t('notifications.' + v) }))} />
      <Switch aria-label={t('notifications.' + event)} checked={value.events[event].enabled} disabled={disabled} onChange={(enabled: boolean) => onChange({ ...value, events: { ...value.events, [event]: { ...value.events[event], enabled } } })} />
    </div>)}</section>
    </div>;
  const channelView = <div className="notification-channels">
    <div className="notification-section-heading"><div><h3>{t('notifications.channels')}</h3><p>{t('notifications.' + (variant === 'settings' ? 'channelsHint' : 'taskChannelsHint'))}</p></div>{variant === 'settings' && <Button onClick={() => setConnecting('feishu')}>{t('notifications.connectTitle')}</Button>}</div>
    {loading && <Spin size="small" />}
    {error && <Alert type="error" message={t('notifications.loadFailed')} action={<Button onClick={() => setRefresh(n => n + 1)}>{t('notifications.retry')}</Button>} />}
    <section className="notification-surface">{channels.map(channel => {
      const rule = value.channels[channel];
      const account = accounts.find(a => a.id === rule?.account_id && a.provider === channel);
      const available = channel === 'desktop' ? (isDesktopRuntime() || browserNotificationsSupported()) : channel === 'feishu' && rule?.account_id ? account?.status === 'connected' : accounts.some(a => a.provider === channel && a.status === 'connected');
      return <div key={channel} className="notification-channel-block"><div className="notification-row notification-channel">
        <ChannelBrand channel={channel} avatar={account?.avatar_url} />
        <div className="notification-grow"><strong>{t('notifications.' + channel)}</strong> <Tag>{t('notifications.' + (channel === 'desktop' ? 'systemChannel' : available ? 'connected' : 'notConnected'))}</Tag>
          {channel === 'desktop' && !isDesktopRuntime() ? <BrowserPermission /> : <p>{channel === 'desktop' ? t('notifications.desktopHint') : rule?.account_id ? `${account ? channelAccountLabel(account) : t('notifications.accountUnavailable')} · ${rule.recipient_id || t('notifications.chooseRecipient')}` : t('notifications.' + channel + 'Hint')}</p>}
          {rule?.account_id && account?.status !== 'connected' && !loading && <small className="notification-warning">{t('notifications.unavailable')}</small>}
        </div>
        {channel !== 'desktop' && <Button disabled={disabled || loading} onClick={() => available ? setPicker(channel) : setConnecting(channel)}>{t('notifications.' + (available ? 'configure' : 'connect'))}</Button>}
        {(available || rule?.enabled || rule?.account_id) && <Switch aria-label={t('notifications.' + channel)} checked={Boolean(rule?.enabled)} disabled={disabled || (loading && channel !== 'desktop') || (!available && !rule?.enabled)} onChange={(enabled: boolean) => {
          if (enabled && variant === 'task' && channel !== 'desktop') { onChange({ ...value, channels: { ...value.channels, [channel]: { ...rule, enabled } } }); return; }
          if (enabled && channel !== 'desktop' && (!rule?.account_id || !rule.recipient_id || account?.status !== 'connected')) { setPicker(channel); return; }
          onChange({ ...value, channels: { ...value.channels, [channel]: { ...rule, enabled } } });
        }} />}
      </div>
      {variant === 'task' && channel !== 'desktop' && rule?.enabled && available && <TargetPicker key={channel} disabled={disabled} inline provider={channel} accounts={accounts.filter(a => a.provider === channel)} current={rule} onClose={() => {}} onSave={target => onChange({ ...value, channels: { ...value.channels, [channel]: target } })} />}
      </div>;
    })}</section></div>;
  return <div className={`notification-rules is-${variant}`}>
    {variant === 'settings' ? <>{channelView}{eventView}</> : <>{eventView}{channelView}</>}
    {picker && <TargetPicker key={picker} disabled={disabled} provider={picker} accounts={accounts.filter(a => a.provider === picker)} current={pickerAccount || value.channels[picker]} onClose={() => { setPicker(undefined); setPickerAccount(undefined); }} onSave={target => { onChange({ ...value, channels: { ...value.channels, [picker]: target } }); setPicker(undefined); setPickerAccount(undefined); }} />}
    <Modal width={1100} open={Boolean(connecting)} destroyOnClose title={t('notifications.connectTitle')} footer={<Button onClick={() => { setConnecting(undefined); setRefresh(n => n + 1); }}>{t('notifications.return')}</Button>} onCancel={() => { setConnecting(undefined); setRefresh(n => n + 1); }}>
      {connecting && <TerminalConnectionPage key={connecting} embedded initialProvider={connecting} onUseAccount={account => {
        setConnecting(undefined); setAccounts(old => [...old.filter(a => a.id !== account.id), account]); setRefresh(n => n + 1);
        if (variant === 'settings') { setPickerAccount({ enabled: true, account_id: account.id }); setPicker(account.provider as ChannelProvider); }
        else onChange({ ...value, channels: { ...value.channels, [account.provider]: { enabled: true, account_id: account.id } } });
      }} />}
    </Modal>
  </div>;
}
