import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useFeishuAccounts } from './useFeishuAccounts';

const mocks = vi.hoisted(() => ({
  list: vi.fn(), update: vi.fn(), success: vi.fn(), warning: vi.fn(),
  t: (key: string) => key, form: {}, navigate: vi.fn(),
  oauth: { clearOauthAttempt: vi.fn() },
}));
vi.mock('antd', () => ({ Form: { useForm: () => [mocks.form] }, Modal: {}, message: mocks }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('react-router-dom', () => ({ useNavigate: () => mocks.navigate }));
vi.mock('@/modules/dataSource/api/clients', () => ({ dataSourceCloudOauthApi: {
  listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.list,
  updateConnectionApiAuthserviceV1CloudConnectionsConnectionIdPut: mocks.update,
} }));
vi.mock('./useFeishuOAuthFlow', () => ({ useFeishuOAuthFlow: () => mocks.oauth }));
vi.mock('@/modules/dataSource/hooks/management/createOAuthEngine', () => ({ startFeishuCLISession: vi.fn() }));
vi.mock('@/runtime/cloud/session', () => ({ getCloudSession: () => null, isCloudBusinessAvailable: () => false }));
vi.mock('@/modules/dataSource/common/feishuOAuth', () => ({
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL: 'fixture', consumeFeishuDataSourceOAuthResult: () => null,
  getFeishuDataSourceCallbackUrl: () => 'https://example.test/callback',
}));

const connection = {
  connection_id: 'fixture-account', status: 'ACTIVE', display_name: 'Fixture',
  provider_options: { chat_enabled: false }, can_use_chat: false,
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.list.mockResolvedValue({ data: { items: [connection] } });
});

it('uses the saved server result and prevents duplicate requests while saving', async () => {
  let finish!: (value: unknown) => void;
  mocks.update.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const { result } = renderHook(() => useFeishuAccounts());
  await waitFor(() => expect(result.current.accounts).toHaveLength(1));
  act(() => {
    result.current.handleToggleChat(result.current.accounts[0], true);
    result.current.handleToggleChat(result.current.accounts[0], true);
  });
  expect(mocks.update).toHaveBeenCalledTimes(1);
  expect(result.current.accounts[0].canUseChat).toBeUndefined();
  expect(result.current.chatUpdatingAccountIds).toEqual(['fixture-account']);
  await act(async () => finish({ data: { data: {
    ...connection, status: 'EXPIRED', provider_options: { chat_enabled: true }, can_use_chat: false,
  } } }));
  expect(result.current.accounts[0]).toMatchObject({ chatEnabled: true, status: 'expired', canUseChat: false });
  expect(result.current.chatUpdatingAccountIds).toEqual([]);
});

it('restores the preference and availability when saving fails', async () => {
  mocks.update.mockRejectedValue(new Error('fixture failure'));
  const { result } = renderHook(() => useFeishuAccounts());
  await waitFor(() => expect(result.current.accounts).toHaveLength(1));
  await act(async () => result.current.handleToggleChat(result.current.accounts[0], true));
  expect(result.current.accounts[0]).toMatchObject({ chatEnabled: false, canUseChat: false });
  expect(result.current.chatUpdatingAccountIds).toEqual([]);
  expect(mocks.success).not.toHaveBeenCalled();
});
