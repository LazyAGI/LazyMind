import { describe, expect, it } from 'vitest';
import type { CloudConnectionResponse } from '@/api/generated/auth-client';
import {
  getCloudConnectionItems,
  mapCloudConnectionToFeishuAccount,
} from './cloudConnection';
import {
  getCloudConnectionItems as getDataSourceConnectionItems,
  mapCloudConnectionToFeishuAccount as mapDataSourceFeishuAccount,
  mapCloudConnectionToNotionAccount,
} from './dataSourceConnection';

describe('Feishu mapper entry point compatibility', () => {
  it.each([
    ['legacy_byo', 'local', { provider_account_meta: { client_id: 'cli_fixture' } }],
    ['managed_oauth', 'cloud', { provider_account_meta: { appid: 'cli_fixture' } }],
    ['cli_personal_app', 'cli_sidecar', { app_id: 'cli_fixture' }],
  ] as const)('preserves all connection fields for %s through either entry point', (method, location, metadata) => {
    const connection: CloudConnectionResponse = {
      connection_id: 'fixture-account', status: 'EXPIRED',
      tenant_id: '', provider: 'feishu', auth_mode: 'tenant', created_at: '2026-09-21T00:00:00Z',
      ...metadata, connection_method: method, credential_location: location,
      provider_account_id: 'ou_fixture', display_name: 'Fixture account',
      provider_options: { chat_enabled: true }, can_use_chat: false,
      scope: 'docs:read,docs:write', provider_tenant_key: 'tenant-fixture',
    };
    const expected = {
      appId: 'cli_fixture', name: 'Fixture account', status: 'expired',
      chatEnabled: true, canUseChat: false,
      connection_method: method, credential_location: location,
      connection: {
        connectionId: 'fixture-account', connectionMethod: method, status: 'expired',
        openId: 'ou_fixture', tenantKey: 'tenant-fixture', grantedScopes: ['docs:read', 'docs:write'],
      },
    };
    expect(mapCloudConnectionToFeishuAccount(connection)).toMatchObject(expected);
    expect(mapDataSourceFeishuAccount(connection)).toEqual(mapCloudConnectionToFeishuAccount(connection));
  });

  it('preserves cached legacy credentials without treating an open ID as the application ID', () => {
    const connection: CloudConnectionResponse = {
      connection_id: 'fixture-account', status: 'ACTIVE',
      tenant_id: '', provider: 'feishu', auth_mode: 'tenant', created_at: '2026-09-21T00:00:00Z',
      provider_account_id: 'ou_fixture', provider_options: { chat_enabled: false }, can_use_chat: false,
    };
    const cached = [{ ...mapCloudConnectionToFeishuAccount(connection),
      appId: 'cli_cached', appSecret: 'fake-secret', name: 'Cached name', chatEnabled: true }];
    const account = mapDataSourceFeishuAccount(connection, cached);
    expect(account).toEqual(mapCloudConnectionToFeishuAccount(connection, cached));
    expect(account).toMatchObject({ appId: 'cli_cached', appSecret: 'fake-secret', name: 'Cached name',
      chatEnabled: false, canUseChat: false, connection_method: 'legacy_byo', credential_location: 'local' });
  });

  it.each([{ items: [] }, { data: { items: [{ connection_id: 'fixture-account' }] } }, {}])(
    'unwraps connection lists consistently through either entry point', (payload) => {
      expect(getDataSourceConnectionItems(payload)).toEqual(getCloudConnectionItems(payload));
    },
  );
});

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
