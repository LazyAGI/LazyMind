import type { ChannelAccount } from '@/modules/channelGateway/api';
import type { Target } from './api';

export type WeChatNotificationStatus = 'connected' | 'pending' | 'reauthorization' | 'unavailable' | 'disconnected' | 'targetUnavailable' | 'checking' | 'unconfigured';

export function wechatAccountStatus(account?: ChannelAccount, targetAvailable?: boolean): WeChatNotificationStatus {
  if (account?.capabilities?.connection_error_code === 'WECHAT_REAUTHORIZATION_REQUIRED') return 'reauthorization';
  if (!account || account.status !== 'connected') return 'disconnected';
  if (targetAvailable === false) return 'pending';
  if (targetAvailable === undefined && (account.capabilities?.notification_ready === false
    || account.capabilities?.connection_check_status === 'activation_required')) return 'pending';
  if (account.capabilities?.connection_check_status === 'unavailable' || ['degraded', 'failed'].includes(account.runtime_status)) return 'unavailable';
  return 'connected';
}

export function wechatTargetStatus(account?: ChannelAccount, target?: Target): WeChatNotificationStatus {
  const accountStatus = wechatAccountStatus(account);
  if (!account || accountStatus === 'disconnected' || accountStatus === 'reauthorization') return accountStatus;
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
  return ({ connected: 'connected', pending: 'pendingActivation', reauthorization: 'wechatReconnectRequired',
    unavailable: 'wechatConnectionUnavailable', disconnected: 'notConnected', targetUnavailable: 'wechatTargetUnavailable', checking: 'wechatChecking', unconfigured: 'wechatRecipientUnconfirmed' })[status];
}

export function wechatStatusHint(status: WeChatNotificationStatus): string {
  return ({ connected: 'connected', pending: 'pendingActivationHint', reauthorization: 'wechatReconnectHint',
    unavailable: 'wechatConnectionUnavailableHint', disconnected: 'wechatReconnectHint', targetUnavailable: 'wechatTargetUnavailableHint', checking: 'wechatCheckingHint', unconfigured: 'wechatRecipientUnconfirmedHint' })[status];
}
