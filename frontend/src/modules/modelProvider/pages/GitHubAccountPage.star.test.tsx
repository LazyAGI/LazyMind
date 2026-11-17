import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { CloudDocumentProvidersVm } from '../hooks/useCloudDocumentProviders';
import GitHubAccountPage from './GitHubAccountPage';
import { writeGitHubStar } from '@/modules/githubStar/api';

const { navigate, identity } = vi.hoisted(() => ({ navigate: vi.fn(), identity: { userId: 'owner-1' } }));
vi.mock('react-router-dom', async original => ({
  ...await original<typeof import('react-router-dom')>(), useNavigate: () => navigate,
}));
vi.mock('@/components/auth', () => ({
  AgentAppsAuth: { getUserInfo: () => ({ userId: identity.userId }), getSessionIdentity: () => identity.userId,
    getAuthHeaders: () => ({}) }, AUTH_USER_CHANGE_EVENT: 'fixture-user-change', AUTH_LOGOUT_EVENT: 'fixture-logout',
}));
vi.mock('@/modules/githubStar/api', async original => ({
  ...await original<typeof import('@/modules/githubStar/api')>(), writeGitHubStar: vi.fn(),
}));
let vm: CloudDocumentProvidersVm;
vi.mock('../hooks/useCloudDocumentProviders', () => ({ useCloudDocumentProviders: () => vm }));

const words: Record<string, string> = {
  authorizationTitle: '连接 GitHub', quickMode: '快捷授权', manual: '手动 OAuth（高级）',
  cancel: '取消授权', start: '开始连接', addAccount: '添加账号', web: '使用网页授权',
  accountManagementTitle: 'GitHub 账号管理', deviceHint: '输入 LazyMind 提供的设备码',
  waiting: '完成浏览器授权后，这里会自动更新。', openBrowser: '打开 GitHub 授权页面',
};
const opportunity = { task_id: 'root-3', conversation_id: 'conversation-1',
  status: 'succeeded', result_ready: true, source: 'final_result' };
const returnIntent = { userId: 'owner-1', returnTo: '/task-center', opportunity };

beforeEach(() => {
  vi.clearAllMocks();
  identity.userId = 'owner-1';
  vm = {
    t: (key: string) => words[key.split('.').pop()!] || key,
    loading: false, githubDialogOpen: true, githubDialogMode: 'quick', setGitHubDialogOpen: vi.fn(),
    setGitHubDialogMode: vi.fn(), setFeishuSetupModalOpen: vi.fn(), setFeishuSetupIntent: vi.fn(),
    handleOpenGitHubSetup: vi.fn(), handleManageGitHubAuth: vi.fn(),
    github: { accounts: [], updating: [], active: false, retryCreate: false, notice: '', session: null,
      cancel: vi.fn().mockResolvedValue(undefined), retry: vi.fn(), start: vi.fn(), openBrowser: vi.fn(), refresh: vi.fn() },
  } as unknown as CloudDocumentProvidersVm;
});
afterEach(cleanup);

function page(intent: unknown = returnIntent) {
  return <MemoryRouter initialEntries={[{ pathname: '/cloud-documents/github', state: { githubStarReturn: intent } }]}>
    <ConfigProvider theme={{ token: { motion: false } }}><GitHubAccountPage /></ConfigProvider>
  </MemoryRouter>;
}

it('Star intent uses the existing local-probe then web-fallback CLI path, after user confirmation', () => {
  render(page());
  expect(vm.github.start).not.toHaveBeenCalled();
  fireEvent.click(within(screen.getByRole('dialog', { name: '连接 GitHub' })).getByRole('button', { name: '开始连接' }));
  expect(vm.github.start).toHaveBeenCalledWith('', false);
  expect(writeGitHubStar).not.toHaveBeenCalled();
});

it('without local credentials the existing CLI page displays the supplied device code and waits', () => {
  vm.github.active = true;
  vm.github.session = { session_id: 'fixture-session', status: 'WAITING_USER',
    user_code: 'ABCD-EFGH', verification_uri: 'https://github.com/login/device' };
  render(page());
  expect(screen.getByText('ABCD-EFGH')).toBeInTheDocument();
  expect(screen.getByText('输入 LazyMind 提供的设备码')).toBeInTheDocument();
  expect(writeGitHubStar).not.toHaveBeenCalled();
  expect(navigate).not.toHaveBeenCalled();
});

it('a completion without a stable connection ID cannot pick an arbitrary active account', async () => {
  const view = render(page());
  vm.github.accounts = [{ connection_id: 'conn-1', provider: 'github', provider_account_id: '10001', status: 'ACTIVE' } as never];
  vm.github.session = { session_id: '', status: 'COMPLETED' };
  await act(async () => view.rerender(page()));
  expect(navigate).not.toHaveBeenCalled();
  expect(writeGitHubStar).not.toHaveBeenCalled();
});

it.each(['CANCELLED', 'EXPIRED', 'FAILED'])('%s cannot resume a Star intent as authenticated', async status => {
  vm.github.session = { session_id: 'fixture-session', status };
  render(page());
  await act(async () => {});
  expect(writeGitHubStar).not.toHaveBeenCalled();
  expect(navigate).not.toHaveBeenCalled();
});

it.each([
  { ...returnIntent, returnTo: 'https://example.test/steal' },
  { ...returnIntent, returnTo: '//example.test/steal' },
  { ...returnIntent, userId: 'other-owner' },
])('an invalid return or foreign identity cannot consume a completed connection: %j', async intent => {
  vm.github.session = { session_id: 'fixture-session', status: 'COMPLETED', connection_id: 'conn-1' };
  render(page(intent));
  await act(async () => {});
  expect(navigate).not.toHaveBeenCalled();
  expect(writeGitHubStar).not.toHaveBeenCalled();
});

it('ordinary account management preserves its explicit new-web-authorization behavior', () => {
  render(page(null));
  fireEvent.click(within(screen.getByRole('dialog', { name: '连接 GitHub' })).getByRole('button', { name: '开始连接' }));
  expect(vm.github.start).toHaveBeenCalledWith('', true);
  expect(writeGitHubStar).not.toHaveBeenCalled();
});
