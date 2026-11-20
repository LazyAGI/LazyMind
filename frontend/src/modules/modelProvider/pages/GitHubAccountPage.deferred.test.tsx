import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CloudDocumentProvidersVm } from '../hooks/useCloudDocumentProviders';
import GitHubAccountPage from './GitHubAccountPage';
import * as api from '@/modules/githubStar/api';

const mocks = vi.hoisted(() => ({
  readDeferredGitHubStarIntent: vi.fn(), acceptDeferredGitHubStarIntent: vi.fn(), cancelDeferredGitHubStarIntent: vi.fn(),
  identity: { owner: 'owner-1', session: 'owner-1:session-1' }, navigate: vi.fn(),
}));
vi.mock('@/modules/githubStar/api', async original => ({
  ...await original<typeof import('@/modules/githubStar/api')>(),
  readDeferredGitHubStarIntent: mocks.readDeferredGitHubStarIntent,
  acceptDeferredGitHubStarIntent: mocks.acceptDeferredGitHubStarIntent,
  cancelDeferredGitHubStarIntent: mocks.cancelDeferredGitHubStarIntent,
  readGitHubStarAccounts: vi.fn(), readLocalGitHubStarCapability: vi.fn(),
  writeGitHubStar: vi.fn(), writeLocalGitHubStar: vi.fn(), markGitHubStarSupported: vi.fn(), claimGitHubStarPrompt: vi.fn(),
}));
vi.mock('@/components/auth', () => ({
  AgentAppsAuth: { getUserInfo: () => ({ userId: mocks.identity.owner }), getSessionIdentity: () => mocks.identity.session,
    getAuthHeaders: () => ({}) }, AUTH_USER_CHANGE_EVENT: 'fixture-user-change', AUTH_LOGOUT_EVENT: 'fixture-logout',
}));
vi.mock('react-router-dom', async original => ({
  ...await original<typeof import('react-router-dom')>(), useNavigate: () => mocks.navigate,
}));
const words: Record<string, string> = {
  supportProject: '支持项目', star: '为项目点 Star', thankYou: '感谢您的支持！',
  cancelSupportRequest: '取消本次支持请求', close: '关闭', cancel: '取消',
  title: '支持 LazyMind', account: 'GitHub 账号', selectAccount: '选择 GitHub 账号',
  accountManagementTitle: 'GitHub 账号管理', addAccount: '添加账号',
  success: '感谢支持，已点 Star', alreadyStarred: '你已为项目点过 Star，感谢支持',
};
function translate(key: string) { return words[key.split('.').pop()!] || key; }
vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => translate(key) }),
}));
let vm: CloudDocumentProvidersVm;
vi.mock('../hooks/useCloudDocumentProviders', () => ({ useCloudDocumentProviders: () => vm }));

const repository = 'LazyAGI/LazyMind';
type Status = 'pending' | 'needs_attention' | 'succeeded' | 'cancelled' | 'exhausted';
function history(status: Status = 'pending') {
  return { intent_id: 'intent-owner-1', status, source: 'local' as const, connection_id: null, account_id: null,
    created_at: '2026-11-17T00:00:00Z', expires_at: '2026-11-24T00:00:00Z', attempt_count: 1,
    next_attempt_at: status === 'pending' ? '2026-11-17T06:00:00Z' : null, rate_limit_not_before: null,
    reason: status === 'needs_attention' ? 'binding_not_captured' : '',
    remote_state: status === 'succeeded' ? 'starred' as const : 'unknown' as const };
}
let response: { repository: string; can_receive: boolean; local_supported: boolean;
  accounts: { connection_id: string; account_id: string; login: string }[]; intent: ReturnType<typeof history> | null };

function page() {
  return <MemoryRouter initialEntries={['/cloud-documents/github']}>
    <ConfigProvider theme={{ token: { motion: false } }}><GitHubAccountPage /></ConfigProvider>
  </MemoryRouter>;
}
async function support() {
  await act(async () => fireEvent.click(screen.getByRole('button', { name: '支持项目' })));
  // The page may use the existing explicit confirmation dialog before accepting.
  const confirm = screen.queryByRole('button', { name: '为项目点 Star' });
  if (confirm) await act(async () => fireEvent.click(confirm));
}
function expectNoImmediateWriteOrAuth() {
  expect(api.writeGitHubStar).not.toHaveBeenCalled(); expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
  expect(vm.github.start).not.toHaveBeenCalled(); expect(vm.github.retry).not.toHaveBeenCalled();
}

beforeEach(() => {
  vi.resetAllMocks(); sessionStorage.clear(); localStorage.clear();
  mocks.identity.owner = 'owner-1'; mocks.identity.session = 'owner-1:session-1';
  response = { repository, can_receive: true, local_supported: true, accounts: [], intent: null };
  mocks.readDeferredGitHubStarIntent.mockImplementation(async () => ({ ...response }));
  mocks.acceptDeferredGitHubStarIntent.mockImplementation(async () => {
    response = { ...response, intent: history('needs_attention') };
    return { repository, accepted: true, intent: response.intent };
  });
  mocks.cancelDeferredGitHubStarIntent.mockImplementation(async () => {
    response = { ...response, intent: history('cancelled') };
    return { repository, intent: response.intent };
  });
  vi.mocked(api.readGitHubStarAccounts).mockRejectedValue(new Error('fixture GitHub offline'));
  vi.mocked(api.readLocalGitHubStarCapability).mockRejectedValue(new Error('fixture GitHub offline'));
  vi.mocked(api.markGitHubStarSupported).mockResolvedValue(undefined);
  vm = {
    t: translate, loading: false, githubDialogOpen: false, githubDialogMode: 'quick',
    setGitHubDialogOpen: vi.fn(), setGitHubDialogMode: vi.fn(), setFeishuSetupModalOpen: vi.fn(), setFeishuSetupIntent: vi.fn(),
    handleOpenGitHubSetup: vi.fn(), handleManageGitHubAuth: vi.fn(),
    github: { accounts: [], updating: [], active: false, retryCreate: false, notice: '', session: null,
      cancel: vi.fn().mockResolvedValue(undefined), retry: vi.fn(), start: vi.fn(), openBrowser: vi.fn(), refresh: vi.fn() },
  } as unknown as CloudDocumentProvidersVm;
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('voluntary support from the real GitHub connection page', () => {
  it('offers support without a Work event, GitHub connection, online capability, or authentication', async () => {
    render(page()); await act(async () => {});
    expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '支持项目' })).toBeEnabled();
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled(); expectNoImmediateWriteOrAuth();
    await support();
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledWith({ source: 'local', confirmed: true });
    expect(screen.getByText('感谢您的支持！')).toBeInTheDocument();
    expect(screen.queryByText(/已点\s*Star|已为项目点过\s*Star|排队|网络恢复|稍后自动|延迟执行/)).not.toBeInTheDocument();
    expectNoImmediateWriteOrAuth(); expect(api.markGitHubStarSupported).toHaveBeenCalled();
  });

  it('only thanks a confirmation after the local service has durably accepted it', async () => {
    let finish!: (value: { repository: string; accepted: true; intent: ReturnType<typeof history> }) => void;
    mocks.acceptDeferredGitHubStarIntent.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    render(page()); await act(async () => {});
    await support();
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('感谢您的支持！')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    await act(async () => finish({ repository, accepted: true, intent: history() }));
    expect(screen.getByText('感谢您的支持！')).toBeInTheDocument(); expectNoImmediateWriteOrAuth();
  });

  it('does not turn a failed persistence request into thanks or a GitHub write', async () => {
    mocks.acceptDeferredGitHubStarIntent.mockRejectedValue(new Error('fixture persistence unavailable'));
    render(page()); await act(async () => {}); await support();
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('感谢您的支持！')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled(); expectNoImmediateWriteOrAuth();
  });

  it('lets the owner explicitly cancel a pending request without deleting history or performing Unstar', async () => {
    response.intent = history();
    render(page()); await act(async () => {});
    expect(mocks.cancelDeferredGitHubStarIntent).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '取消本次支持请求' })));
    expect(mocks.cancelDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(mocks.cancelDeferredGitHubStarIntent).toHaveBeenCalledWith('intent-owner-1');
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrAuth();
    cleanup(); render(page()); await act(async () => {});
    expect(response.intent?.status).toBe('cancelled');
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrAuth();
  });

  it('unmounting the page after acceptance does not cancel the persistent request', async () => {
    const view = render(page()); await act(async () => {}); await support();
    expect(screen.getByText('感谢您的支持！')).toBeInTheDocument();
    view.unmount();
    expect(mocks.cancelDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrAuth();
  });

  it.each<Status>(['pending', 'needs_attention', 'succeeded', 'cancelled', 'exhausted'])(
    '%s history is displayed only as a voluntary page state without an automatic invitation or renewed budget', async status => {
      response.intent = history(status);
      render(page()); await act(async () => {});
      expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalled();
      expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
      expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrAuth();
      expect(screen.queryByText(/排队|网络恢复|稍后自动|延迟执行/)).not.toBeInTheDocument();
    });

  it('an old owner history response cannot expose cancellation in a newly logged-in session', async () => {
    let finish!: (value: typeof response) => void;
    const old = { ...response, intent: history() };
    mocks.readDeferredGitHubStarIntent.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }))
      .mockResolvedValue({ ...response, intent: null });
    const view = render(page()); await act(async () => {});
    expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    mocks.identity.owner = 'owner-2'; mocks.identity.session = 'owner-2:session-1';
    await act(async () => {
      window.dispatchEvent(new CustomEvent('fixture-user-change'));
      view.rerender(page());
    });
    await act(async () => finish(old));
    expect(screen.queryByRole('button', { name: '取消本次支持请求' })).not.toBeInTheDocument();
    expect(mocks.cancelDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrAuth();
  });
});
