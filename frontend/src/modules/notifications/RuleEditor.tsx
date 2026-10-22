import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Modal, Select, Spin, Switch, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { channelAccountLabel, isChannelAccountAvailable, listChannelAccounts, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';
import { TerminalConnectionPage } from '@/modules/channelGateway/pages/ChannelConnectionPage';
import { ArrowRightOutlined, CheckCircleOutlined, ExclamationCircleOutlined, PauseCircleOutlined } from '@ant-design/icons';
import { isDesktopRuntime } from '@/runtime/mode';
import { channelGateEnabled, channels, events, providers, type ChannelName, type NotificationConfig, type Preferences } from './api';
import TargetPicker from './TargetPicker';
import ChannelBrand from './ChannelBrand';
import BrowserPermission from './BrowserPermission';
import { desktopNotificationsAuthorized } from './browser';
import useWeChatNotificationStatus from './useWeChatNotificationStatus';
import { wechatStatusHint, wechatStatusLabel } from './wechatNotificationStatus';
import './index.scss';

export default function RuleEditor({ value, onChange, disabled = false, variant = 'settings', preferences, onOpenSettings, highlightedChannel, connectionInPlace = false, accountsRefresh = 0 }: {
  value: NotificationConfig; onChange: (config: NotificationConfig) => void; disabled?: boolean; variant?: 'settings' | 'task';
  preferences?: Preferences; onOpenSettings?: (channel?: ChannelName) => void; highlightedChannel?: ChannelName; connectionInPlace?: boolean;
  accountsRefresh?: number;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [connectionProvider, setConnectionProvider] = useState<ChannelProvider>();
  const [authorizationErrors, setAuthorizationErrors] = useState<Record<string, boolean>>({});
  const updateAuthorization = useCallback((accountId: string, required: boolean) => {
    setAuthorizationErrors(old => old[accountId] === required ? old : { ...old, [accountId]: required });
  }, []);
  const updateAccount = useCallback((account: ChannelAccount) => {
    setAccounts(old => old.map(item => item.id === account.id ? account : item));
  }, []);
  const [desktopAuthorized, setDesktopAuthorized] = useState(desktopNotificationsAuthorized);
  // Electron sends notifications from its main process. A website permission
  // describes only the browser consumer and cannot gate the native channel.
  const nativeDesktop = isDesktopRuntime();
  const desktopAvailable = nativeDesktop || desktopAuthorized;
  const watchWechat = Boolean(value.channels.wechat?.enabled) && preferences?.enabled !== false
    && (variant === 'settings' || !preferences || channelGateEnabled(preferences, 'wechat'));
  const wechat = useWeChatNotificationStatus({
    enabled: watchWechat, ready: !loading && !error,
    accountId: variant === 'task' ? value.channels.wechat?.account_id : undefined,
    recipientId: variant === 'task' ? value.channels.wechat?.recipient_id : undefined,
    refreshKey: JSON.stringify([refresh, accountsRefresh]),
    aggregate: variant === 'settings',
  });
  const openConnection = (provider: ChannelProvider = 'feishu') => {
    if (connectionInPlace || variant === 'task') setConnectionProvider(provider);
    else navigate(`/settings?section=channels&provider=${provider}`);
  };
  const closeConnection = () => { setConnectionProvider(undefined); setRefresh(n => n + 1); };
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    Promise.all(providers.map(provider => listChannelAccounts(provider))).then(results => { if (active) setAccounts(results.flatMap(r => r.items)); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [refresh, accountsRefresh]);
  useEffect(() => {
    const updated = wechat.account;
    if (updated) setAccounts(old => old.map(account => account.id === updated.id ? updated : account));
  }, [wechat.account]);
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
  const renderChannel = (channel: ChannelName) => {
      const rule = value.channels[channel];
      const account = variant === 'task' ? accounts.find(a => a.id === rule?.account_id && a.provider === channel) : undefined;
      const providerAccounts = accounts.filter(a => a.provider === channel);
      const availableAccounts = providerAccounts.filter(channel === 'wechat' ? account => account.status === 'connected' : isChannelAccountAvailable);
      const hasAvailableAccount = availableAccounts.length > 0;
      const available = channel === 'desktop' ? desktopAvailable : variant === 'task' && rule?.account_id ? Boolean(account && (channel === 'wechat' ? account.status === 'connected' : isChannelAccountAvailable(account))) : hasAvailableAccount;
      const canConfigure = channel === 'desktop' ? desktopAvailable : hasAvailableAccount;
      const globallyDisabled = variant === 'task' && Boolean(preferences && !channelGateEnabled(preferences, channel));
      const channelDisabled = disabled || globallyDisabled;
      const authorizationRequired = channel === 'wecom' && Boolean(rule?.account_id && authorizationErrors[rule.account_id]);
      const wechatState = channel === 'wechat' ? wechat.status ?? (available ? 'unconfigured' : 'disconnected') : undefined;
      const wechatReauthorizationRequired = wechatState === 'reauthorization';
      const pendingActivation = wechatState === 'pending';
      const wechatNeedsAttention = wechatState && !['connected', 'checking', 'unconfigured'].includes(wechatState);
      const notificationOff = !rule?.enabled || globallyDisabled || preferences?.enabled === false;
      const statusStyle = notificationOff ? 'off' : channel === 'desktop' ? nativeDesktop ? 'managed' : desktopAuthorized ? 'connected' : 'disconnected' : pendingActivation ? 'pending' : wechatNeedsAttention || authorizationRequired ? 'expired' : available ? 'connected' : 'disconnected';
      const statusLabel = notificationOff ? 'off' : channel === 'desktop' ? nativeDesktop ? 'systemManaged' : desktopAuthorized ? 'authorized' : 'notAuthorized' : wechatState ? wechatStatusLabel(wechatState) : authorizationRequired ? 'wecomAuthorizationExpired' : available ? 'connected' : 'notConnected';
      return <div key={channel} className={`notification-channel-block is-${channel}${globallyDisabled ? ' is-globally-disabled' : ''}${highlightedChannel === channel ? ' is-requested' : ''}`}><div className="notification-row notification-channel">
        <ChannelBrand channel={channel} avatar={account?.avatar_url} />
        <div className="notification-grow"><strong>{t('notifications.' + channel)}</strong> <Tag className={`notification-status is-${statusStyle}`}>{t('notifications.' + statusLabel)}</Tag>
          <p>{channel === 'desktop' ? t('notifications.desktopHint') : variant === 'settings' ? t('notifications.' + channel + 'Hint') : rule?.account_id ? `${account ? channelAccountLabel(account, accounts) : t('notifications.accountUnavailable')} · ${rule.recipient_id || t('notifications.chooseRecipient')}` : t('notifications.' + channel + 'Hint')}</p>
          {channel === 'desktop' && (nativeDesktop ? <p>{t('notifications.nativeDesktopHint')}</p> : <BrowserPermission />)}
          {variant === 'task' && channel !== 'desktop' && !notificationOff && rule?.account_id && !available && !pendingActivation && !loading && <small className="notification-warning">{t('notifications.unavailable')}</small>}
        </div>
        {channel !== 'desktop' && !canConfigure && <Button className="notification-configure-action" icon={<ArrowRightOutlined />} disabled={channelDisabled || loading} onClick={() => openConnection(channel)}>{t('notifications.connect')}</Button>}
        {(variant === 'task' || canConfigure || rule?.enabled) && <Switch aria-label={t('notifications.' + channel)} checked={Boolean(rule?.enabled)} disabled={channelDisabled || (loading && channel !== 'desktop') || (!canConfigure && !rule?.enabled)} onChange={(enabled: boolean) => {
          onChange({ ...value, channels: { ...value.channels, [channel]: variant === 'settings' ? { enabled } : { ...rule, enabled } } });
        }} />}
      </div>
      {channel === 'wechat' && wechatNeedsAttention && watchWechat && !loading && <Alert
        className="notification-activation-warning" type="warning" showIcon
        message={t(pendingActivation ? 'notifications.wechatActivationRequired' : wechatState === 'targetUnavailable' ? 'notifications.wechatTargetUnavailable' : 'notifications.wechatConnectionIssue')}
        description={t('notifications.' + wechatStatusHint(wechatState!))}
        action={wechatReauthorizationRequired || wechatState === 'disconnected'
          ? <Button disabled={disabled} onClick={() => openConnection('wechat')}>{t('notifications.connect')}</Button>
          : <Button disabled={disabled} onClick={() => setRefresh(n => n + 1)}>{t('notifications.reload')}</Button>}
      />}
      {channel === 'wechat' && wechatState === 'unconfigured' && watchWechat && !loading && <Alert
        className="notification-activation-warning" type="info" showIcon
        message={t('notifications.wechatRecipientUnconfirmed')} description={t('notifications.wechatRecipientUnconfirmedHint')}
      />}
      {globallyDisabled && <div className="notification-channel-gate"><p>{t('notifications.channelDisabledHint')}</p><div>
        {onOpenSettings && <Button type="link" disabled={disabled} aria-label={`${t('notifications.openSettingsEnable')} · ${t('notifications.' + channel)}`} onClick={() => onOpenSettings(channel)}>{t('notifications.openSettingsEnable')}</Button>}
      </div></div>}
      {variant === 'task' && channel !== 'desktop' && rule?.enabled && (canConfigure || channel === 'wechat' && Boolean(account)) && <TargetPicker key={channel} disabled={channelDisabled} inline provider={channel} accounts={providerAccounts} refreshKey={channel === 'wechat' ? JSON.stringify([refresh, accountsRefresh, wechatState]) : undefined} onAccountConnected={updateAccount} onAuthorizationChange={updateAuthorization} current={rule} onClose={() => {}} onSave={target => {
        onChange({ ...value, channels: { ...value.channels, [channel]: target } });
      }} />}
      </div>;
  };
  const channelView = <div className="notification-channels">
    <div className="notification-section-heading"><div><h3>{t('notifications.channels')}</h3><p>{t('notifications.' + (variant === 'settings' ? 'channelsHint' : 'taskChannelsHint'))}</p></div></div>
    {loading && <Spin size="small" />}
    {error && <Alert type="error" message={t('notifications.loadFailed')} action={<Button onClick={() => setRefresh(n => n + 1)}>{t('notifications.retry')}</Button>} />}
    {variant === 'settings' ? <div className="notification-channel-scopes">
      <section className="notification-channel-scope" aria-label={t('notifications.allTasksScope')}>
        <header><h4>{t('notifications.allTasksScope')}</h4><p>{t('notifications.allTasksScopeHint')}</p></header>
        <div className="notification-surface">{renderChannel('desktop')}</div>
      </section>
      <section className="notification-channel-scope" aria-label={t('notifications.scheduledTasksScope')}>
        <header><h4>{t('notifications.scheduledTasksScope')}</h4><p>{t('notifications.scheduledTasksScopeHint')}</p></header>
        {!loading && !error && !accounts.some(account => account.provider === 'wechat' ? account.status === 'connected' : isChannelAccountAvailable(account)) && <Alert type="warning" showIcon message={t('notifications.noExternalChannels')} />}
        <div className="notification-surface">{providers.map(renderChannel)}</div>
        <footer><Button type="link" className="notification-link-action" disabled={disabled} onClick={() => openConnection()}>{t('notifications.connectTitle')}<ArrowRightOutlined /></Button></footer>
      </section>
    </div> : <section className="notification-surface">{channels.map(renderChannel)}</section>}
  </div>;
  return <div className={`notification-rules is-${variant}`}>
    {variant === 'settings' ? <>{channelView}{eventView}</> : <>{eventView}{channelView}</>}
    <Modal zIndex={1500} width={1100} open={Boolean(connectionProvider)} destroyOnHidden title={t('notifications.connectTitle')} footer={<Button onClick={closeConnection}>{t('notifications.return')}</Button>} onCancel={closeConnection}>
      {connectionProvider && <TerminalConnectionPage initialProvider={connectionProvider} embedded />}
    </Modal>
  </div>;
}
