import { describe, expect, it } from 'vitest';
import { channelAccountLabel, type ChannelAccount } from './api';

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
  });
});
