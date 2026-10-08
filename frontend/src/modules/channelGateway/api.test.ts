import { beforeEach, describe, expect, it, vi } from 'vitest';
import { cancelConnectionSession, createConnectionSession, refreshConnectionSession, submitConnectionChallenge, channelAccountLabel, type ChannelAccount } from './api';

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
