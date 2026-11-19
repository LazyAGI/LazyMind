import { channelAccountStatusKey, type ChannelAccount } from '@/modules/channelGateway/api';
import type { Target } from './api';

export type WeChatNotificationStatus = 'connected' | 'connecting' | 'pending' | 'reauthorization' | 'unavailable' | 'disconnected' | 'targetUnavailable' | 'checking' | 'unconfigured';

export function wechatAccountStatus(account?: ChannelAccount, targetAvailable?: boolean): WeChatNotificationStatus {
  const status = channelAccountStatusKey(account);
  if (status === 'wechatReconnectRequired') return 'reauthorization';
  if (status === 'wechatConnectionUnavailable') return 'unavailable';
  if (status === 'connecting') return 'connecting';
  if (status === 'notConnected' || status === 'unbound') return 'disconnected';
  if (account?.capabilities?.connection_check_status === 'unavailable') return 'unavailable';
  if (targetAvailable === false) return 'pending';
  if (targetAvailable === undefined && status === 'pendingActivation') return 'pending';
  return 'connected';
}

export function wechatTargetStatus(account?: ChannelAccount, target?: Target): WeChatNotificationStatus {
  const accountStatus = wechatAccountStatus(account);
  if (!account || accountStatus === 'disconnected' || accountStatus === 'reauthorization' || accountStatus === 'connecting') return accountStatus;
  if (!target) return 'targetUnavailable';
  if (target.connection_error_code === 'WECHAT_REAUTHORIZATION_REQUIRED') return 'reauthorization';
  if (target.connection_check_status === 'unavailable') return 'unavailable';
  // Account summaries describe the last checked recipient. Prefer the exact
  // recipient's result while retaining account-wide login and runtime errors.
  return wechatAccountStatus({ ...account, capabilities: { ...account.capabilities,
    ...(target.connection_check_status ? { connection_check_status: target.connection_check_status,
      connection_error_code: target.connection_error_code || '' } : {}),
  } }, target.available);
}

export function wechatStatusLabel(status: WeChatNotificationStatus): string {
  return ({ connected: 'connected', connecting: 'connecting', pending: 'pendingActivation', reauthorization: 'notConnected',
    unavailable: 'unavailable', disconnected: 'notConnected', targetUnavailable: 'wechatTargetUnavailable', checking: 'wechatChecking', unconfigured: 'wechatRecipientUnconfirmed' })[status];
}

export function wechatStatusHint(status: WeChatNotificationStatus): string {
  return ({ connected: 'connected', connecting: 'connectingHint', pending: 'pendingActivationHint', reauthorization: 'wechatReconnectHint',
    unavailable: 'wechatConnectionUnavailableHint', disconnected: 'wechatReconnectHint', targetUnavailable: 'wechatTargetUnavailableHint', checking: 'wechatCheckingHint', unconfigured: 'wechatRecipientUnconfirmedHint' })[status];
}

export function wechatEditorStatus({ accounts, status, accountId, recipientId }: {
  accounts: ChannelAccount[]; status?: WeChatNotificationStatus;
  accountId?: string; recipientId?: string;
}): WeChatNotificationStatus {
  // Live checks describe confirmed recipients only. An incomplete draft must
  // use its selected account, never a different account's recipient status.
  if (!accountId) {
    const states = accounts.map(account => wechatAccountStatus(account));
    if (states.some(state => state === 'connected' || state === 'pending')) return 'unconfigured';
    return (['connecting', 'reauthorization', 'unavailable'] as const).find(state => states.includes(state)) ?? 'disconnected';
  }
  if (recipientId && status && status !== 'unconfigured') return status;
  const accountStatus = wechatAccountStatus(accounts.find(account => account.id === accountId));
  // Pausing checks does not clear the configured recipient. Without a live
  // result, show the account state and reserve recipient guidance for drafts.
  return accountStatus === 'connected' && !recipientId ? 'unconfigured' : accountStatus;
}
