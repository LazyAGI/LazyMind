import type { RawAxiosRequestConfig } from 'axios';
import i18n from '@/i18n';
import {
  ChannelAccountsApiFactory,
  Configuration,
  ConnectionSessionsApiFactory,
  type AccountListView,
  type AccountView,
  type ChallengeView as GeneratedChallengeView,
  type ConnectionSessionView,
  type QRCodeView as GeneratedQRCodeView,
  type SessionErrorView as GeneratedSessionErrorView,
} from '@/api/generated/channel-gateway-client';
import { axiosInstance, BASE_URL } from '@/components/request';

const configuration = new Configuration({ basePath: BASE_URL });
const channelAccountsApi = ChannelAccountsApiFactory(
  configuration,
  BASE_URL,
  axiosInstance,
);
const connectionSessionsApi = ConnectionSessionsApiFactory(
  configuration,
  BASE_URL,
  axiosInstance,
);

export type ChannelProvider = 'wechat' | 'feishu' | 'wecom';
export type ConnectionSessionStatus = ConnectionSessionView['status'];
export type ConnectionAllowedAction = ConnectionSessionView['allowed_actions'][number];
export type ChannelAccount = AccountView & {
  avatar_url?: string | null;
  default_recipient_id?: string;
  capabilities?: { notification_ready?: boolean } & Record<string, unknown>;
};
export type ChannelAccountList = AccountListView;
export type QRCodeView = GeneratedQRCodeView;
export type ChallengeView = GeneratedChallengeView;
export type SessionErrorView = GeneratedSessionErrorView;
export type ConnectionSession = Omit<ConnectionSessionView, 'mode'> & { mode: 'qr_code' | 'credentials' };

export async function listChannelAccounts(
  provider: ChannelProvider,
): Promise<ChannelAccountList> {
  const response = await channelAccountsApi.listChannelAccounts({ provider });
  return response.data;
}

export async function disconnectChannelAccount(accountId: string): Promise<void> {
  await channelAccountsApi.disconnectChannelAccount({ accountId });
}

export async function pauseChannelAccount(accountId: string): Promise<void> {
  await channelAccountsApi.pauseChannelAccount({ accountId });
}

export async function resumeChannelAccount(accountId: string, options?: { silentError?: boolean }): Promise<ChannelAccount> {
  const requestOptions: RawAxiosRequestConfig & { silentError?: boolean } = options || {};
  const response = await channelAccountsApi.resumeChannelAccount({ accountId }, requestOptions);
  return response.data;
}

export async function archiveChannelAccount(accountId: string): Promise<void> {
  await channelAccountsApi.archiveChannelAccount({ accountId });
}

export async function renameChannelAccount(accountId: string, label: string): Promise<ChannelAccount> {
  const response = await channelAccountsApi.renameChannelAccount({ accountId, renameChannelAccountRequest: { label } });
  return response.data;
}

export function channelAccountLabel(account: ChannelAccount, peers: ChannelAccount[] = []): string {
  if (account.provider === 'wecom') {
    const name = account.label.trim() || i18n.t('notifications.wecomDefaultBotName');
    const botId = account.identity?.bot_id?.trim();
    const duplicates = [...new Map([...peers, account].filter(peer => peer.provider === 'wecom' && (peer.label.trim() || i18n.t('notifications.wecomDefaultBotName')) === name).map(peer => [peer.id, peer])).values()];
    if (!botId && duplicates.length < 2) return name;
    const identity = botId || account.id;
    let length = Math.min(6, identity.length);
    while (length < identity.length && duplicates.some(peer => {
      const otherIdentity = peer.identity?.bot_id?.trim() || peer.id;
      return otherIdentity !== identity && otherIdentity.slice(-length) === identity.slice(-length);
    })) length = Math.min(length + 2, identity.length);
    return i18n.t('notifications.wecomAccountIdentityLabel', { name, identifier: identity.slice(-length) });
  }
  if (account.provider !== 'feishu') return account.label;
  const storedLabel = account.label.trim();
  const generatedPrefix = '飞书 · ';
  const candidate = storedLabel.startsWith(generatedPrefix)
    ? storedLabel.slice(generatedPrefix.length).trim()
    : storedLabel;
  const internalId = /^(?:ou|cli|ca)_[A-Za-z0-9_-]+$/;
  if (!candidate || candidate === '飞书' || internalId.test(candidate)) {
    return '飞书账号';
  }
  return storedLabel;
}

export function isChannelAccountPendingActivation(account: ChannelAccount): boolean {
  return account.provider === 'wechat'
    && account.status === 'connected'
    && account.capabilities?.notification_ready === false;
}

export function isChannelAccountAvailable(account: ChannelAccount): boolean {
  return account.status === 'connected' && !isChannelAccountPendingActivation(account);
}

export async function createConnectionSession(
  provider: ChannelProvider,
  options?: { createNew?: boolean; reauthorize?: boolean; idempotencyKey?: string; accountId?: string; credentials?: { bot_id: string; secret: string }; silentError?: boolean },
): Promise<ConnectionSession> {
  const requestOptions: RawAxiosRequestConfig & { silentError?: boolean } = {
    headers: options?.idempotencyKey ? { 'Idempotency-Key': options.idempotencyKey } : undefined,
    silentError: options?.silentError,
  };
  const response = await axiosInstance.post<ConnectionSession>(`${BASE_URL}/api/channel-gateway/v1/connection-sessions`, {
    provider,
    ...(options?.reauthorize ? { reauthorize: true } : {}),
    ...(options?.createNew ? { create_new: true } : {}),
    ...(options?.accountId ? { account_id: options.accountId } : {}),
    ...(options?.credentials ? { credentials: options.credentials } : {}),
  }, requestOptions);
  return response.data;
}

export async function getConnectionSession(
  sessionId: string,
): Promise<ConnectionSession> {
  const response = await connectionSessionsApi.getConnectionSession({ sessionId });
  return response.data;
}

export async function submitConnectionChallenge(
  sessionId: string,
  value: string,
  type = 'numeric_code',
  options?: { silentError?: boolean },
): Promise<ConnectionSession> {
  const requestOptions: RawAxiosRequestConfig & { silentError?: boolean } = options || {};
  const response = await connectionSessionsApi.submitConnectionChallenge({
    sessionId,
    connectionChallengeSubmit: { type, value },
  }, requestOptions);
  return response.data;
}

export async function refreshConnectionSession(
  sessionId: string,
  options?: { silentError?: boolean },
): Promise<ConnectionSession> {
  const requestOptions: RawAxiosRequestConfig & { silentError?: boolean } = options || {};
  const response = await connectionSessionsApi.refreshConnectionSession({ sessionId }, requestOptions);
  return response.data;
}

export async function cancelConnectionSession(sessionId: string, options?: { silentError?: boolean }): Promise<void> {
  const requestOptions: RawAxiosRequestConfig & { silentError?: boolean } = options || {};
  await connectionSessionsApi.cancelConnectionSession({ sessionId }, requestOptions);
}

export async function listNotificationGroups(accountId: string, cursor = '') {
  return (await channelAccountsApi.listNotificationGroups({ accountId, cursor, limit: 100 })).data;
}
