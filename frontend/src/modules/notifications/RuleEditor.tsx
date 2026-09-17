import { useEffect, useState } from 'react';
import { Alert, Button, Modal, Select, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';
import { listChannelAccounts, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';
import { TerminalConnectionPage } from '@/modules/channelGateway';
import { isDesktopRuntime } from '@/runtime/mode';
import { channels, events, getTargets, providers, type ChannelRule, type NotificationConfig, type Target } from './api';
import ChannelBrand from './ChannelBrand';
import './index.scss';

function TargetPicker({ provider, accounts, current, onSave, onClose }: {
  provider: ChannelProvider; accounts: ChannelAccount[]; current?: ChannelRule;
  onSave: (target: ChannelRule) => void; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [accountId, setAccountId] = useState(current?.account_id);
  const [recipientId, setRecipientId] = useState(current?.recipient_id);
  const [targets, setTargets] = useState<Target[]>([]);
  const [cursor, setCursor] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let active = true;
    setTargets([]); setCursor(''); setError(false);
    if (!accountId) return;
    setLoading(true);
    getTargets(accountId).then(page => { if (active) { setTargets(page.items); setCursor(page.next_cursor); } }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [accountId, refresh]);
  return <Modal open title={`${t('notifications.configure')} · ${t('notifications.' + provider)}`} onCancel={onClose} onOk={() => onSave({ enabled: true, account_id: accountId, recipient_id: recipientId })} okText={t('notifications.save')} cancelText={t('notifications.cancel')} okButtonProps={{ disabled: loading || error || !accounts.some(a => a.id === accountId && a.status === 'connected') || !targets.some(target => target.recipient_id === recipientId && target.available) }}>
    <div className="notification-target-picker">
      <label>{t('notifications.account')}<Select disabled={loading} aria-label={t('notifications.account')} value={accountId} placeholder={t('notifications.chooseAccount')} onChange={(value: string) => { setAccountId(value); setRecipientId(undefined); }} options={accounts.filter(a => a.status === 'connected').map(a => ({ value: a.id, label: a.label }))} /></label>
      <label>{t('notifications.recipient')}<Select aria-label={t('notifications.recipient')} value={recipientId} placeholder={t('notifications.chooseRecipient')} loading={loading} disabled={!accountId || loading} onChange={setRecipientId} options={targets.map(target => ({ value: target.recipient_id, label: target.label, disabled: !target.available }))} /></label>
      {!loading && accountId && !targets.some(target => target.available) && <p>{t('notifications.noTargets')}</p>}
      {error && <Alert type="error" message={t('notifications.loadFailed')} />}
      <Button disabled={!accountId || loading} onClick={() => setRefresh(n => n + 1)}>{t('notifications.refresh')}</Button>
      {cursor && <Button disabled={loading} onClick={async () => {
        if (!accountId) return;
        setLoading(true);
        try { const page = await getTargets(accountId, cursor); setTargets(old => [...old, ...page.items]); setCursor(page.next_cursor); } catch { setError(true); } finally { setLoading(false); }
      }}>{t('notifications.loadMore')}</Button>}
    </div>
  </Modal>;
}

export default function RuleEditor({ value, onChange, disabled = false }: {
  value: NotificationConfig; onChange: (config: NotificationConfig) => void; disabled?: boolean;
}) {
  const { t } = useTranslation();
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [picker, setPicker] = useState<ChannelProvider>();
  const [connecting, setConnecting] = useState<ChannelProvider>();
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    Promise.all(providers.map(listChannelAccounts)).then(results => { if (active) setAccounts(results.flatMap(r => r.items)); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [refresh]);
  return <div className="notification-rules">
    <h3>{t('notifications.events')}</h3>
    <section className="notification-surface">{events.map(event => <div className="notification-row" key={event}>
      <div><strong>{t('notifications.' + event)}</strong></div>
      <Select aria-label={`${t('notifications.' + event)} · ${t('notifications.content')}`} disabled={disabled || !value.events[event].enabled} value={value.events[event].content} onChange={(content: 'summary' | 'full') => onChange({ ...value, events: { ...value.events, [event]: { ...value.events[event], content } } })} options={['summary', 'full'].map(v => ({ value: v, label: t('notifications.' + v) }))} />
      <Switch aria-label={t('notifications.' + event)} checked={value.events[event].enabled} disabled={disabled} onChange={(enabled: boolean) => onChange({ ...value, events: { ...value.events, [event]: { ...value.events[event], enabled } } })} />
    </div>)}</section>
    <h3>{t('notifications.channels')}</h3>
    {loading && <Spin size="small" />}
    {error && <Alert type="error" message={t('notifications.loadFailed')} action={<Button onClick={() => setRefresh(n => n + 1)}>{t('notifications.retry')}</Button>} />}
    <section className="notification-surface">{channels.map(channel => {
      const rule = value.channels[channel];
      const account = accounts.find(a => a.id === rule?.account_id && a.provider === channel);
      const available = channel === 'desktop' ? isDesktopRuntime() : accounts.some(a => a.provider === channel && a.status === 'connected');
      return <div className="notification-row notification-channel" key={channel}>
        <ChannelBrand channel={channel} avatar={account?.avatar_url} />
        <div className="notification-grow"><strong>{t('notifications.' + channel)}</strong>
          <p>{channel === 'desktop' ? t('notifications.' + (isDesktopRuntime() ? 'desktopHint' : 'desktopUnsupported')) : rule?.account_id ? `${account?.label || rule.account_id} · ${rule.recipient_id || t('notifications.chooseRecipient')}` : t('notifications.chooseAccount')}</p>
          {rule?.account_id && account?.status !== 'connected' && !loading && <small className="notification-warning">{t('notifications.unavailable')}</small>}
        </div>
        {channel !== 'desktop' && <Button disabled={disabled || loading} onClick={() => available ? setPicker(channel) : setConnecting(channel)}>{t('notifications.' + (available ? 'configure' : 'connect'))}</Button>}
        {(available || rule?.enabled || rule?.account_id) && <Switch aria-label={t('notifications.' + channel)} checked={Boolean(rule?.enabled)} disabled={disabled || (loading && channel !== 'desktop') || (!available && !rule?.enabled)} onChange={(enabled: boolean) => {
          if (enabled && channel !== 'desktop' && (!rule?.account_id || !rule.recipient_id || account?.status !== 'connected')) { setPicker(channel); return; }
          onChange({ ...value, channels: { ...value.channels, [channel]: { ...rule, enabled } } });
        }} />}
      </div>;
    })}</section>
    {picker && <TargetPicker key={picker} provider={picker} accounts={accounts.filter(a => a.provider === picker)} current={value.channels[picker]} onClose={() => setPicker(undefined)} onSave={target => { onChange({ ...value, channels: { ...value.channels, [picker]: target } }); setPicker(undefined); }} />}
    <Modal width={1100} open={Boolean(connecting)} destroyOnClose title={t('notifications.connectTitle')} footer={<Button onClick={() => { setConnecting(undefined); setRefresh(n => n + 1); }}>{t('notifications.return')}</Button>} onCancel={() => { setConnecting(undefined); setRefresh(n => n + 1); }}>
      {connecting && <TerminalConnectionPage key={connecting} embedded initialProvider={connecting} />}
    </Modal>
  </div>;
}
