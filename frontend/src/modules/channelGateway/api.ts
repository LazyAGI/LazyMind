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
export type ChannelAccount = AccountView & { avatar_url?: string | null };
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

export async function createConnectionSession(
  provider: ChannelProvider,
  options?: { idempotencyKey?: string; accountId?: string; credentials?: { bot_id: string; secret: string } },
): Promise<ConnectionSession> {
  const response = await axiosInstance.post<ConnectionSession>(`${BASE_URL}/api/channel-gateway/v1/connection-sessions`, {
    provider,
    ...(options?.accountId ? { account_id: options.accountId } : {}),
    ...(options?.credentials ? { credentials: options.credentials } : {}),
  }, { headers: options?.idempotencyKey ? { 'Idempotency-Key': options.idempotencyKey } : undefined });
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
): Promise<ConnectionSession> {
  const response = await connectionSessionsApi.submitConnectionChallenge({
    sessionId,
    connectionChallengeSubmit: { type, value },
  });
  return response.data;
}

export async function refreshConnectionSession(
  sessionId: string,
): Promise<ConnectionSession> {
  const response = await connectionSessionsApi.refreshConnectionSession({ sessionId });
  return response.data;
}

export async function cancelConnectionSession(sessionId: string): Promise<void> {
  await connectionSessionsApi.cancelConnectionSession({ sessionId });
}
