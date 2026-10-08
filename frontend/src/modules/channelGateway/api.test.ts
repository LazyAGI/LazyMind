import { beforeEach, describe, expect, it, vi } from 'vitest';
import { cancelConnectionSession, channelAccountLabel, type ChannelAccount } from './api';

const http = vi.hoisted(() => ({ request: vi.fn(), defaults: {} }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => { vi.clearAllMocks(); http.request.mockResolvedValue({ data: {} }); });

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
});
