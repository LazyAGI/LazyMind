import { describe, expect, it } from 'vitest';
import type { CloudConnectionResponse } from '@/api/generated/auth-client';
import { mapCloudConnectionToFeishuAccount } from './cloudConnection';
import {
  mapCloudConnectionToFeishuAccount as mapDataSourceFeishuAccount,
  mapCloudConnectionToNotionAccount,
} from './dataSourceConnection';

describe.each([
  ['Feishu settings', mapCloudConnectionToFeishuAccount],
  ['Feishu data source', mapDataSourceFeishuAccount],
  ['Notion data source', mapCloudConnectionToNotionAccount],
] as const)('%s chat preference', (_name, mapAccount) => {
  it('preserves an enabled preference when authorization expires', () => {
    const connection: CloudConnectionResponse = {
      connection_id: 'fixture-account', status: 'EXPIRED',
      tenant_id: '', provider: 'feishu', auth_mode: 'tenant', created_at: '2026-09-21T00:00:00Z',
      provider_options: { chat_enabled: true }, can_use_chat: false,
    };
    const account = mapAccount(connection);
    expect(account.chatEnabled).toBe(true);
    expect(account.status).toBe('expired');
    expect(account.canUseChat).toBe(false);
  });

  it('reads availability from the server instead of rebuilding it from status and preference', () => {
    const connection: CloudConnectionResponse = {
      connection_id: 'fixture-account', status: 'ACTIVE',
      tenant_id: '', provider: 'feishu', auth_mode: 'tenant', created_at: '2026-09-21T00:00:00Z',
      provider_options: { chat_enabled: true }, can_use_chat: false,
    };
    expect(mapAccount(connection).canUseChat).toBe(false);
    expect(mapAccount({ ...connection, can_use_chat: true }).canUseChat).toBe(true);
    const legacyResponse: Partial<CloudConnectionResponse> = { ...connection };
    delete legacyResponse.can_use_chat;
    expect(mapAccount(legacyResponse as CloudConnectionResponse).canUseChat).toBeUndefined();
  });
});
