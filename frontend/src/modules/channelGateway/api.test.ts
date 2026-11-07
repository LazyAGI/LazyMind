import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { CHANNEL_ACCOUNTS_CHANGED_EVENT, archiveChannelAccount, channelAccountConnectionStatus, channelAccountStatusKey, channelProviderStatusKey, channelStatusLabelKey, channelStatusHintKey, isChannelAccountAvailable, isChannelAccountPendingActivation, cancelConnectionSession, createConnectionSession, listChannelAccounts, refreshConnectionSession, submitConnectionChallenge, channelAccountLabel, type ChannelAccount } from './api';

const http = vi.hoisted(() => ({ request: vi.fn(), post: vi.fn(), defaults: {} }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => { vi.clearAllMocks(); http.request.mockResolvedValue({ data: {} }); http.post.mockResolvedValue({ data: {} }); });

function feishu(label: string, authorizedName = ''): ChannelAccount {
  return {
    id: 'ca_internal', provider: 'feishu', label,
    status: 'connected', runtime_status: 'running', updated_at: '2026-09-20',
    identity: {
      app_id: 'cli_internal', authorized_name: authorizedName,
      authorized_id: 'ou_internal',
    },
  } as ChannelAccount;
}

describe('channelAccountLabel', () => {
  it('does not present the authorized user as the robot name', () => {
    expect(channelAccountLabel(feishu('飞书 · ou_internal', 'Alice'))).toBe('飞书账号');
  });

  it('uses a generic name instead of internal ids when the user name is unavailable', () => {
    expect(channelAccountLabel(feishu('飞书 · ou_internal'))).toBe('飞书账号');
  });

  it('preserves a user-defined account name', () => {
    expect(channelAccountLabel(feishu('研究协作助手', 'Alice'))).toBe('研究协作助手');
    expect(channelAccountLabel(feishu('飞书 · 项目组', 'Alice'))).toBe('飞书 · 项目组');
  });

  it('localizes generated Feishu names and preserves custom remarks in English', async () => {
    const language = i18n.language;
    try {
      await i18n.changeLanguage('en-US');
      expect(channelAccountLabel(feishu('飞书 · ou_internal'))).toBe('Feishu account');
      expect(channelAccountLabel(feishu('飞书用户ABC123 的 LazyMind 助手'))).toBe('LazyMind assistant for Feishu user ABC123');
      expect(channelAccountLabel(feishu('研究协作助手'))).toBe('研究协作助手');
    } finally {
      await i18n.changeLanguage(language);
    }
  });
});

it('passes quiet cleanup errors through the generated cancellation request', async () => {
  await cancelConnectionSession('session/a', { silentError: true });
  expect(http.request.mock.lastCall![0]).toMatchObject({
    method: 'DELETE', url: '/api/channel-gateway/v1/connection-sessions/session%2Fa', silentError: true,
  });
  await cancelConnectionSession('session/a');
  expect(http.request.mock.lastCall![0].silentError).toBeUndefined();
  await refreshConnectionSession('session/a', { silentError: true });
  expect(http.request.mock.lastCall![0].silentError).toBe(true);
  await submitConnectionChallenge('session/a', '1234', 'numeric_code', { silentError: true });
  expect(http.request.mock.lastCall![0].silentError).toBe(true);
});

it('passes quiet activation checks through the generated account list request', async () => {
  await listChannelAccounts('wechat', { silentError: true });
  expect(http.request.mock.lastCall![0]).toMatchObject({
    method: 'GET', url: '/api/channel-gateway/v1/channel-accounts?provider=wechat', silentError: true,
  });
  await listChannelAccounts('wechat');
  expect(http.request.mock.lastCall![0].silentError).toBeUndefined();
});

it('serializes the read-only connection check through the generated account list contract', async () => {
  await listChannelAccounts('wechat', { silentError: true, checkConnection: true });
  expect(http.request.mock.lastCall![0]).toMatchObject({
    method: 'GET', url: '/api/channel-gateway/v1/channel-accounts?provider=wechat&check_connection=true',
    silentError: true,
  });
  expect(http.request.mock.lastCall![0]).not.toHaveProperty('checkConnection');
  expect(http.request.mock.lastCall![0]).not.toHaveProperty('params');
  await listChannelAccounts('wechat');
  expect(http.request.mock.lastCall![0].url).not.toContain('check_connection');
});

it('lets the active panel own QR creation errors without a global navigation toast', async () => {
  await createConnectionSession('wechat', { idempotencyKey: 'operation', silentError: true });
  expect(http.post).toHaveBeenCalledWith('/api/channel-gateway/v1/connection-sessions', { provider: 'wechat' }, {
    headers: { 'Idempotency-Key': 'operation' }, silentError: true,
  });
});


it('identifies WeCom robots by their original Bot ID rather than list order', () => {
  const a = { id: 'a', provider: 'wecom', label: '日报机器人', identity: { bot_id: 'bot-original-A8C4F2' } } as ChannelAccount;
  const b = { ...a, id: 'b', identity: { bot_id: 'bot-another-B9D5E3' } };
  expect(channelAccountLabel(a, [b, a])).toBe('日报机器人 · A8C4F2');
  expect(channelAccountLabel(b, [a, b])).toBe('日报机器人 · B9D5E3');
  expect(channelAccountLabel(a, [a])).toBe(channelAccountLabel(a, [b, a]));
  expect(channelAccountLabel({ ...a, id: 'reauthorized-account' }, [b])).toBe(channelAccountLabel(a, [b]));
  expect(a.label).toBe('日报机器人');
});

it('extends robot identity hints when the last six characters collide', () => {
  const a = { id: 'a', provider: 'wecom', label: '日报机器人', identity: { bot_id: 'bot-AAA8C4F2' } } as ChannelAccount;
  const b = { ...a, id: 'b', identity: { bot_id: 'bot-BBA8C4F2' } };
  expect(channelAccountLabel(a, [a, b])).toBe('日报机器人 · AAA8C4F2');
  expect(channelAccountLabel(b, [b, a])).toBe('日报机器人 · BBA8C4F2');
});

it('keeps legacy accounts usable before robot identity metadata is available', () => {
  const a = { id: 'legacy-a', provider: 'wecom', label: '旧机器人' } as ChannelAccount;
  expect(channelAccountLabel(a)).toBe('旧机器人');
  expect(channelAccountLabel(a, [a, { ...a, id: 'legacy-b' }])).not.toContain('连接');
});


it.each(['stopped', 'failed', 'degraded', 'unsupported'] as const)('does not show a %s runtime as connected', runtime_status => {
  const account = { ...feishu('Bot'), runtime_status } as ChannelAccount;
  expect(channelAccountConnectionStatus(account)).toBe('disconnected');
  expect(isChannelAccountAvailable(account)).toBe(false);
});

it('refreshes other configuration screens only after removal succeeds', async () => {
  const changed = vi.fn();
  window.addEventListener(CHANNEL_ACCOUNTS_CHANGED_EVENT, changed);
  try {
    http.request.mockRejectedValueOnce(new Error('unavailable'));
    await expect(archiveChannelAccount('robot')).rejects.toThrow('unavailable');
    expect(changed).not.toHaveBeenCalled();
    await archiveChannelAccount('robot');
    expect(changed).toHaveBeenCalledTimes(1);
    expect(http.request.mock.lastCall![0]).toMatchObject({ method: 'POST', url: '/api/channel-gateway/v1/channel-accounts/robot:archive' });
  } finally {
    window.removeEventListener(CHANNEL_ACCOUNTS_CHANGED_EVENT, changed);
  }
});

it('keeps a Feishu message connection available when only its bot menu is degraded', () => {
  const account = { ...feishu('Bot'), runtime_status: 'degraded', capabilities: { connection_ready: true, bot_menu_ready: false } } as ChannelAccount;
  expect(channelAccountConnectionStatus(account)).toBe('connected');
  expect(isChannelAccountAvailable(account)).toBe(true);
  expect(channelAccountConnectionStatus({ ...account, status: 'disconnected' })).toBe('disconnected');
  expect(channelAccountConnectionStatus({ ...account, runtime_status: 'failed' })).toBe('disconnected');
  expect(channelAccountConnectionStatus({ ...account, capabilities: { connection_ready: false } })).toBe('disconnected');
  expect(channelAccountConnectionStatus({ ...account, provider: 'wecom' })).toBe('disconnected');
});

it('distinguishes WeCom authorization requirements from a network disconnection', () => {
  const account = { ...feishu('Bot'), provider: 'wecom', binding_status: 'connected', capabilities: { authorization_required: true } } as ChannelAccount;
  expect(channelAccountConnectionStatus(account)).toBe('authorization_required');
  expect(isChannelAccountAvailable(account)).toBe(false);
  expect(channelAccountConnectionStatus({ ...account, capabilities: { authorization_required: false } })).toBe('connected');
  expect(channelAccountConnectionStatus({ ...account, binding_status: 'paused', status: 'disconnected' })).toBe('disconnected');
  expect(channelAccountConnectionStatus({ ...account, capabilities: {}, runtime_status: 'failed' })).toBe('disconnected');
});


describe('shared connection status labels', () => {
  it.each(['disconnected', 'stopped', 'failed'] as const)('uses notConnected for a %s account', state => {
    const account = { ...feishu('Bot'), ...(state === 'disconnected' ? { status: state } : { runtime_status: state }) } as ChannelAccount;
    expect(channelAccountStatusKey(account)).toBe('notConnected');
    expect(channelProviderStatusKey([account])).toBe('notConnected');
  });
  it('recognizes activation metadata without requiring a second flag', () => {
    const account = { ...feishu('Bot'), provider: 'wechat', capabilities: { connection_check_status: 'activation_required' } } as ChannelAccount;
    expect(isChannelAccountPendingActivation(account)).toBe(true);
    expect(isChannelAccountAvailable(account)).toBe(false);
    expect(channelAccountStatusKey(account)).toBe('pendingActivation');
  });
  it.each([
    { capabilities: { connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED', notification_ready: false }, expected: 'wechatReconnectRequired' },
  ])('does not show $expected as connected or pending activation', ({ capabilities, expected }) => {
    const account = { ...feishu('Bot'), provider: 'wechat', capabilities } as ChannelAccount;
    expect(isChannelAccountPendingActivation(account)).toBe(false);
    expect(isChannelAccountAvailable(account)).toBe(false);
    expect(channelAccountStatusKey(account)).toBe(expected);
    expect(channelProviderStatusKey([account])).toBe(expected);
  });
  it('keeps an aggregate connected when another account remains usable', () => {
    const offline = { ...feishu('Offline'), status: 'disconnected' } as ChannelAccount;
    expect(channelProviderStatusKey([offline, feishu('Online')])).toBe('connected');
    expect(channelAccountStatusKey(offline)).toBe('notConnected');
  });
});

it('keeps recipient check failures separate from the account transport connection', () => {
  const account = { ...feishu('Bot'), provider: 'wechat', capabilities: { connection_check_status: 'unavailable', notification_ready: false } } as ChannelAccount;
  expect(channelAccountStatusKey(account)).toBe('connected');
  expect(isChannelAccountPendingActivation(account)).toBe(false);
});


it('uses one primary label while preserving the actionable connection cause', () => {
  const expired = { ...feishu('Bot'), provider: 'wechat', capabilities: { connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED' } } as ChannelAccount;
  const cause = channelAccountStatusKey(expired);
  expect(cause).toBe('wechatReconnectRequired');
  expect(channelStatusLabelKey(cause)).toBe('notConnected');
  expect(channelStatusHintKey(cause)).toBe('wechatReconnectHint');
  expect(channelStatusLabelKey('wecomAuthorizationExpired')).toBe('notAuthorized');
  expect(channelStatusHintKey('wecomAuthorizationExpired')).toBe('wecomReauthorizeStepHint');
  expect(channelStatusLabelKey('wechatConnectionUnavailable')).toBe('unavailable');
  expect(channelStatusHintKey('wechatConnectionUnavailable')).toBe('wechatConnectionUnavailableHint');
});
