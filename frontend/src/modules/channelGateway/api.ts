import { axiosInstance, BASE_URL } from '@/components/request';

const CHANNEL_GATEWAY_V1 = `${BASE_URL}/api/channel-gateway/v1`;

export type ChannelProvider = 'wechat' | 'feishu';

export type ConnectionSessionStatus =
  | 'preparing'
  | 'waiting_scan'
  | 'scanned'
  | 'verification_required'
  | 'confirming'
  | 'connected'
  | 'expired'
  | 'canceled'
  | 'failed'
  | string;

export type ConnectionAllowedAction = 'cancel' | 'submit_challenge' | 'refresh' | string;

export interface ChannelAccount {
  id: string;
  provider: string;
  label: string;
  status: string;
  runtime_status: string;
  connected_at: string | null;
  last_poll_at: string | null;
  last_message_at: string | null;
  last_error: string | null;
  updated_at: string;
}

export interface ChannelAccountList {
  items: ChannelAccount[];
}

export interface QRCodeView {
  payload: string;
  version: number;
  expires_at: string;
}

export interface ChallengeView {
  type: string;
  prompt: string;
  input_mode: string;
}

export interface SessionErrorView {
  code: string;
  message: string;
  retryable: boolean;
}

export interface ConnectionSession {
  id: string;
  provider: string;
  mode: string;
  status: ConnectionSessionStatus;
  revision: number;
  message: string;
  qr: QRCodeView | null;
  challenge: ChallengeView | null;
  poll_after_ms: number;
  allowed_actions: ConnectionAllowedAction[];
  account: ChannelAccount | null;
  error: SessionErrorView | null;
}

export async function listChannelAccounts(
  provider: ChannelProvider,
): Promise<ChannelAccountList> {
  const resp = await axiosInstance.get<ChannelAccountList>(
    `${CHANNEL_GATEWAY_V1}/channel-accounts`,
    { params: { provider } },
  );
  return resp.data;
}

export async function disconnectChannelAccount(accountId: string): Promise<void> {
  await axiosInstance.delete(
    `${CHANNEL_GATEWAY_V1}/channel-accounts/${encodeURIComponent(accountId)}`,
  );
}

export async function createConnectionSession(
  provider: ChannelProvider,
  options?: { idempotencyKey?: string },
): Promise<ConnectionSession> {
  const headers: Record<string, string> = {};
  if (options?.idempotencyKey) {
    headers['Idempotency-Key'] = options.idempotencyKey;
  }
  const resp = await axiosInstance.post<ConnectionSession>(
    `${CHANNEL_GATEWAY_V1}/connection-sessions`,
    { provider },
    { headers },
  );
  return resp.data;
}

export async function getConnectionSession(
  sessionId: string,
): Promise<ConnectionSession> {
  const resp = await axiosInstance.get<ConnectionSession>(
    `${CHANNEL_GATEWAY_V1}/connection-sessions/${encodeURIComponent(sessionId)}`,
  );
  return resp.data;
}

export async function submitConnectionChallenge(
  sessionId: string,
  value: string,
  type = 'numeric_code',
): Promise<ConnectionSession> {
  const resp = await axiosInstance.post<ConnectionSession>(
    `${CHANNEL_GATEWAY_V1}/connection-sessions/${encodeURIComponent(sessionId)}:submit-challenge`,
    { type, value },
  );
  return resp.data;
}

export async function refreshConnectionSession(
  sessionId: string,
): Promise<ConnectionSession> {
  const resp = await axiosInstance.post<ConnectionSession>(
    `${CHANNEL_GATEWAY_V1}/connection-sessions/${encodeURIComponent(sessionId)}:refresh`,
  );
  return resp.data;
}

export async function cancelConnectionSession(sessionId: string): Promise<void> {
  await axiosInstance.delete(
    `${CHANNEL_GATEWAY_V1}/connection-sessions/${encodeURIComponent(sessionId)}`,
  );
}
