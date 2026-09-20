import { useEffect, useState } from 'react';
import { Alert, Button, Modal, Select } from 'antd';
import { useTranslation } from 'react-i18next';
import { channelAccountLabel, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';
import { getTargets, getGroups, type ChannelRule, type Target } from './api';

export default function TargetPicker({ provider, accounts, current, onSave, onClose, inline = false, disabled = false }: {
  provider: ChannelProvider; accounts: ChannelAccount[]; current?: ChannelRule;
  onSave: (target: ChannelRule) => void; onClose: () => void; inline?: boolean; disabled?: boolean;
}) {
  const { t } = useTranslation();
  const [accountId, setAccountId] = useState(current?.account_id);
  const [recipientId, setRecipientId] = useState(current?.recipient_id);
  const [targets, setTargets] = useState<Target[]>([]);
  const [cursor, setCursor] = useState('');
  const [groupCursor, setGroupCursor] = useState('');
  const [groupError, setGroupError] = useState(false);
  const [groupLoading, setGroupLoading] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let active = true;
    setGroupLoading(false); setTargets([]); setCursor(''); setGroupCursor(''); setError(false); setGroupError(false);
    if (!accountId) return;
    setLoading(true);
    getTargets(accountId).then(page => {
      if (active) { setTargets(old => [...new Map([...page.items, ...old].map(item => [item.recipient_id, item])).values()]); setCursor(page.next_cursor); }
    }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    if (provider === 'feishu') {
      setGroupLoading(true);
      getGroups(accountId).then(page => {
        if (active) { setTargets(old => [...new Map([...old, ...page.items].map(item => [item.recipient_id, item])).values()]); setGroupCursor(page.next_cursor); }
      }).catch(() => { if (active) setGroupError(true); }).finally(() => { if (active) setGroupLoading(false); });
    }
    return () => { active = false; };
  }, [accountId, refresh, provider]);
  useEffect(() => { if (inline) { setAccountId(current?.account_id); setRecipientId(current?.recipient_id); } }, [inline, current?.account_id, current?.recipient_id]);
  useEffect(() => {
    if (loading || recipientId || !accountId) return;
    const defaultId = accounts.find(account => account.id === accountId)?.default_recipient_id;
    if (defaultId && targets.some(target => target.recipient_id === defaultId && target.available)) {
      setRecipientId(defaultId);
      if (inline) onSave({ enabled: true, account_id: accountId, recipient_id: defaultId });
    }
  }, [loading, recipientId, accountId, accounts, targets, inline, onSave]);
  const selectedOrDefault = recipientId || accounts.find(account => account.id === accountId)?.default_recipient_id;
  useEffect(() => {
    let active = true;
    if (accountId && selectedOrDefault) {
      getTargets(accountId, '', selectedOrDefault).then(page => {
        if (active) setTargets(old => [...new Map([...old, ...page.items].map(item => [item.recipient_id, item])).values()]);
      }).catch(() => { if (active) setError(true); });
    }
    return () => { active = false; };
  }, [accountId, selectedOrDefault, refresh]);
  const fields =     <div className="notification-target-picker">
      <label className="notification-target-field">{t('notifications.account')}<Select className="notification-target-select" disabled={disabled || loading} aria-label={t('notifications.account')} value={provider === 'feishu' && !accounts.some(a => a.id === accountId && a.status === 'connected') ? undefined : accountId} placeholder={t('notifications.chooseAccount')} onChange={(value: string) => { setAccountId(value); setRecipientId(undefined); if (inline) onSave({ enabled: true, account_id: value }); }} options={accounts.filter(a => a.status === 'connected').map(a => ({ value: a.id, label: channelAccountLabel(a) }))} /></label>
      <label className="notification-target-field">{t('notifications.recipient')}<Select className="notification-target-select" showSearch optionFilterProp="label" aria-label={t('notifications.recipient')} value={recipientId} placeholder={t('notifications.chooseRecipient')} loading={loading} disabled={disabled || !accountId || loading} onChange={(value: string) => { setRecipientId(value); if (inline) onSave({ enabled: true, account_id: accountId, recipient_id: value }); }} options={targets.map(target => ({ value: target.recipient_id, label: target.kind === 'conversation' ? [t('notifications.directConversation'), target.label !== target.recipient_id ? target.label : ''].filter(Boolean).join(' · ') : target.label, disabled: !target.available }))} /></label>
      {groupLoading && <p>{t('notifications.loadingGroups')}</p>}
      {!loading && !groupLoading && accountId && !targets.some(target => target.available) && <p>{t(provider === 'feishu' ? 'notifications.noGroups' : provider === 'wecom' ? 'notifications.noWecomTargets' : 'notifications.noTargets')}</p>}
      {groupError && <Alert type="warning" message={t('notifications.groupPermissionHint')} />}
      {error && <Alert type="error" message={t('notifications.loadFailed')} />}
      <Button disabled={disabled || !accountId || loading} onClick={() => setRefresh(n => n + 1)}>{t('notifications.refresh')}</Button>
      {(cursor || groupCursor) && <Button disabled={disabled || loading} onClick={async () => {
        if (!accountId) return;
        setLoading(true);
        try {
          if (cursor) { const page = await getTargets(accountId, cursor); setTargets(old => [...new Map([...old, ...page.items].map(item => [item.recipient_id, item])).values()]); setCursor(page.next_cursor); }
          if (groupCursor) { const page = await getGroups(accountId, groupCursor); setTargets(old => [...new Map([...old, ...page.items].map(item => [item.recipient_id, item])).values()]); setGroupCursor(page.next_cursor); }
        } catch { setError(true); } finally { setLoading(false); }
      }}>{t('notifications.loadMore')}</Button>}
    </div>;
  if (inline) return fields;
  return <Modal zIndex={1500} open title={`${t('notifications.configure')} · ${t('notifications.' + provider)}`} onCancel={onClose} onOk={() => onSave({ enabled: true, account_id: accountId, recipient_id: recipientId })} okText={t('notifications.save')} cancelText={t('notifications.cancel')} okButtonProps={{ disabled: disabled || loading || error || !accounts.some(a => a.id === accountId && a.status === 'connected') || !targets.some(target => target.recipient_id === recipientId && target.available) }}>
{fields}</Modal>;
}
