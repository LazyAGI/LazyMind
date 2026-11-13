import { act, cleanup, fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider, Modal, message } from 'antd';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { webcrypto } from 'node:crypto';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { CloudConnectionResponse, GitHubCLISessionResponse } from '@/api/generated/auth-client';
import i18n from '@/i18n';
import GitHubAccountPage from '@/modules/modelProvider/pages/GitHubAccountPage';
import { useGitHubConnections } from '@/modules/modelProvider/hooks/useGitHubConnections';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as starAPI from './api';

const mocks = vi.hoisted(() => ({
  list: vi.fn(), credentials: vi.fn(), start: vi.fn(), poll: vi.fn(), cancel: vi.fn(),
  getAttempt: vi.fn(), cancelAttempt: vi.fn(), open: vi.fn(),
}));
// The real provider VM, CLI hook, account page, router and Star host remain active.
// Isolate their external service/OS clients and unrelated local-source settings.
vi.mock('@/runtime/cloud/session', () => ({
  getCloudSession: async () => null, isCloudBusinessAvailable: () => false,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: 'fixture-cloud-change',
}));
vi.mock('@/modules/modelProvider/hooks/useLocalDataSourceSettings', () => ({
  useLocalDataSourceSettings: () => ({ loading: false, canCreateLocalSource: false, localSourceCount: 0 }),
}));
vi.mock('@/modules/dataSource/api/clients', () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.list,
    getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet: mocks.credentials,
    startGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsPost: mocks.start,
    getGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdGet: mocks.poll,
    cancelGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdDelete: mocks.cancel,
    getGithubCliAttemptApiAuthserviceV1CloudGithubCliAttemptsAttemptIdGet: mocks.getAttempt,
    cancelGithubCliAttemptApiAuthserviceV1CloudGithubCliAttemptsAttemptIdDelete: mocks.cancelAttempt,
  }, dataSourceProviderConnectionsApi: {},
}));
vi.mock('@/modules/chat/utils/request', async original => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => ({ getObsidianPublicationStatus: async () => ({ data: { data: { vaults: [], state: 'unavailable' } } }) }),
}));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(), readGitHubStarAccounts: vi.fn(),
  claimGitHubStarPrompt: vi.fn(), writeGitHubStar: vi.fn(), writeLocalGitHubStar: vi.fn(), markGitHubStarSupported: vi.fn(), findGitHubStarOpportunity: vi.fn(),
}));

const CODE = 'ABCD-EFGH';
const DEVICE_URL = 'https://github.com/login/device';
const target: CloudConnectionResponse = {
  connection_id: 'intended-cli', provider: 'github', provider_account_id: '10001', display_name: 'intended-user',
  auth_mode: 'oauth_user', tenant_id: '', status: 'ACTIVE', connection_method: 'legacy_byo', token_source: 'github_cli',
  created_at: '2026-11-04T00:00:00Z', can_use_chat: false, provider_options: { chat_enabled: false },
};
const other: CloudConnectionResponse = { ...target, connection_id: 'unrelated-cli', provider_account_id: '20002', display_name: 'other-user' };
const opportunity: starAPI.GitHubStarOpportunity = { task_id: 'root-3', conversation_id: 'work-1',
  status: 'succeeded', result_ready: true, source: 'final_result' };
let connections: CloudConnectionResponse[];
let completionRefresh: boolean;
function session(status: string, extra: Partial<GitHubCLISessionResponse> = {}) {
  return { data: { session_id: 'cli-session', status, expires_at: new Date(Date.now() + 900_000).toISOString(), ...extra } };
}
function complete() {
  // A second account appears concurrently; selecting accounts[0] is incorrect.
  connections = [other, target];
  return session('COMPLETED', { connection_id: target.connection_id });
}

beforeEach(async () => {
  vi.resetAllMocks();
  vi.stubEnv('VITE_LAZYMIND_MODE', 'desktop');
  vi.stubGlobal('crypto', webcrypto);
  localStorage.clear(); sessionStorage.clear();
  localStorage.setItem('lazymind:user', JSON.stringify({ userId: 'owner-1', username: 'fixture-owner',
    token: 'fixture-app-token-not-a-credential', sessionId: 'fixture-login' }));
  await i18n.changeLanguage('zh-CN');
  connections = [];
  completionRefresh = false;
  mocks.list.mockImplementation(async ({ provider }: { provider: string }) => {
    if (provider === 'github' && connections.length) completionRefresh = true;
    return { data: { items: provider === 'github' ? connections : [] } };
  });
  mocks.credentials.mockResolvedValue({ data: { app_id: '', secret_configured: false } });
  mocks.start.mockImplementation(async () => complete());
  mocks.poll.mockImplementation(async () => complete());
  mocks.cancel.mockResolvedValue(session('CANCELLED'));
  mocks.open.mockResolvedValue({ opened: true });
  Object.defineProperty(window, 'lazymindDesktop', { configurable: true, value: { openGitHubCLIAuthorization: mocks.open } });
  vi.mocked(starAPI.readGitHubStarAccounts).mockImplementation(async () => ({ repository: 'LazyAGI/LazyMind', accounts: connections.map(item => ({
    connection_id: item.connection_id, account_id: item.provider_account_id!, login: item.display_name!,
    state: 'not_starred' as const, eligible: true,
  })) }));
  vi.mocked(starAPI.markGitHubStarSupported).mockResolvedValue(undefined);
  vi.mocked(starAPI.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  vi.mocked(starAPI.writeGitHubStar).mockResolvedValue({ repository: 'LazyAGI/LazyMind', connection_id: target.connection_id,
    account_id: '10001', login: 'intended-user', state: 'starred', write_performed: true });
  vi.mocked(starAPI.writeLocalGitHubStar).mockImplementation(async () => { throw new Error('Connected-account flow must not probe local credentials'); });
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('Unexpected external request in Star fixture'); }));
});
afterEach(() => {
  cleanup();
  act(() => { Modal.destroyAll(); message.destroy(); });
  vi.useRealTimers(); vi.unstubAllEnvs(); vi.unstubAllGlobals();
  Reflect.deleteProperty(window, 'lazymindDesktop');
});

async function finishHook(kind: 'local' | 'web') {
  if (kind === 'web') mocks.start.mockResolvedValue(session('WAITING_USER', { user_code: CODE, verification_uri: DEVICE_URL }));
  const hook = renderHook(() => useGitHubConnections(vi.fn()));
  if (kind === 'web') vi.useFakeTimers();
  await act(async () => { await hook.result.current.start('', false); });
  if (kind === 'web') {
    expect(hook.result.current.session?.user_code).toBe(CODE);
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    vi.useRealTimers();
  }
  expect(hook.result.current.session?.status).toBe('COMPLETED');
  expect(hook.result.current.accounts.map(item => item.connection_id)).toEqual([other.connection_id, target.connection_id]);
  return hook;
}

it.each(['local', 'web'] as const)('real CLI hook keeps the exact successful %s connection for consumers', async kind => {
  const hook = await finishHook(kind);
  // This assertion exposes finish('COMPLETED') discarding the server's identity.
  expect(hook.result.current.session?.connection_id).toBe(target.connection_id);
  expect(starAPI.writeGitHubStar).not.toHaveBeenCalled();
});

function ReturnedWork() {
  const location = useLocation();
  return <>
    <pre data-testid='returned-state'>{JSON.stringify(location.state)}</pre>
    <GitHubStarPrompt userId='owner-1' blocked={false} returnTo='/task-center' />
  </>;
}

function realPage() {
  return render(<ConfigProvider theme={{ token: { motion: false } }}>
    <MemoryRouter initialEntries={[{ pathname: '/cloud-documents/github', state: {
      githubStarReturn: { userId: 'owner-1', returnTo: '/task-center', opportunity },
    } }]}><Routes>
      <Route path='/cloud-documents/github' element={<GitHubAccountPage />} />
      <Route path='/task-center' element={<ReturnedWork />} />
    </Routes></MemoryRouter>
  </ConfigProvider>);
}

it.each(['local', 'web'] as const)('real %s completion returns the intended account, rechecks and waits for Star confirmation', async kind => {
  if (kind === 'web') mocks.start.mockResolvedValue(session('WAITING_USER', { user_code: CODE, verification_uri: DEVICE_URL }));
  realPage();
  const add = await screen.findByRole('button', { name: '添加账号' });
  await waitFor(() => expect(add).toBeEnabled());
  if (!screen.queryByRole('dialog', { name: '连接 GitHub' })) fireEvent.click(add);
  const dialog = await screen.findByRole('dialog', { name: '连接 GitHub' });
  if (kind === 'web') vi.useFakeTimers();
  await act(async () => fireEvent.click(within(dialog).getByRole('button', { name: '开始连接' })));
  await vi.waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
  if (kind === 'web') {
    expect(screen.getByText(CODE)).toBeInTheDocument();
    expect(mocks.open).toHaveBeenCalledWith(DEVICE_URL);
    expect(starAPI.writeGitHubStar).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    vi.useRealTimers();
  }
  await waitFor(() => expect(completionRefresh).toBe(true));
  const returned = await screen.findByTestId('returned-state');
  expect(JSON.parse(returned.textContent!)).toMatchObject({ githubStarResume: {
    userId: 'owner-1', opportunity, connectionId: target.connection_id,
  } });
  expect(starAPI.readGitHubStarAccounts).toHaveBeenCalled();
  expect(mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody).toMatchObject({ force_web: false });
  expect(starAPI.claimGitHubStarPrompt).not.toHaveBeenCalled();
  expect(starAPI.writeGitHubStar).not.toHaveBeenCalled();
  await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
  expect(starAPI.writeGitHubStar).toHaveBeenCalledTimes(1);
  expect(starAPI.writeGitHubStar).toHaveBeenCalledWith({ connection_id: target.connection_id, expected_account_id: '10001', confirmed: true });
  expect(starAPI.writeLocalGitHubStar).not.toHaveBeenCalled();
});
