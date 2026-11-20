import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as api from './api';
import { publishGitHubStarSupport } from './supportNotifications';

const { identity, navigate } = vi.hoisted(() => ({ identity: { owner: 'owner-1', authOwner: 'owner-1', session: 'owner-1:session-1' }, navigate: vi.fn() }));
vi.mock('@/components/auth', () => ({
  AgentAppsAuth: { getSessionIdentity: () => identity.session, getUserInfo: () => ({ userId: identity.authOwner }), getAuthHeaders: () => ({}) },
  AUTH_USER_CHANGE_EVENT: 'fixture-user-change', AUTH_LOGOUT_EVENT: 'fixture-logout',
}));
vi.mock('react-router-dom', async original => ({ ...await original<typeof import('react-router-dom')>(), useNavigate: () => navigate }));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(), readDeferredGitHubStarIntent: vi.fn(), acceptDeferredGitHubStarIntent: vi.fn(),
  cancelDeferredGitHubStarIntent: vi.fn(), readGitHubStarAccounts: vi.fn(), claimGitHubStarPrompt: vi.fn(),
  findGitHubStarOpportunity: vi.fn(), writeGitHubStar: vi.fn(), writeLocalGitHubStar: vi.fn(), markGitHubStarSupported: vi.fn(),
}));

const repository = 'LazyAGI/LazyMind', repositoryURL = `https://github.com/${repository}`;
const account: api.GitHubStarAccount = { connection_id: 'saved-alice', account_id: '10001', login: 'alice', state: 'not_starred', eligible: true };
const opportunity: api.GitHubStarOpportunity = { task_id: 'root-3', conversation_id: 'result-1', status: 'succeeded', result_ready: true, source: 'final_result' };
const success: api.GitHubStarResult = { repository, ...account, state: 'starred', write_performed: true };
let popup: { closed: boolean; opener: unknown; location: { replace: ReturnType<typeof vi.fn> }; close: ReturnType<typeof vi.fn> };
function host(value: api.GitHubStarOpportunity | null = opportunity) {
  return <MemoryRouter><ConfigProvider theme={{ token: { motion: false } }}>
    <GitHubStarPrompt userId={identity.owner} mode='plugin' opportunity={value} blocked={false} returnTo='/task-center' />
  </ConfigProvider></MemoryRouter>;
}
async function offer() { const view = render(host()); await act(async () => {}); return view; }
async function confirm() { await act(async () => fireEvent.click(screen.getByRole('button', { name: '去 GitHub 点 Star' }))); }
function expectNoDeferredWrite() {
  expect(api.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled();
  expect(api.cancelDeferredGitHubStarIntent).not.toHaveBeenCalled();
}

beforeEach(async () => {
  vi.resetAllMocks(); localStorage.clear(); sessionStorage.clear();
  identity.owner = 'owner-1'; identity.authOwner = 'owner-1'; identity.session = 'owner-1:session-1';
  await i18n.changeLanguage('zh-CN');
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  popup = { closed: false, opener: {}, location: { replace: vi.fn() }, close: vi.fn() };
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window);
  vi.mocked(api.readDeferredGitHubStarIntent).mockResolvedValue({ repository, local_supported: true, can_receive: true, accounts: [account], intent: null });
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [account], can_star: true });
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({ show: true, reason: 'eligible', task_id: opportunity.task_id, min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 });
  vi.mocked(api.writeGitHubStar).mockResolvedValue(success);
  vi.mocked(api.writeLocalGitHubStar).mockResolvedValue({ repository, account_id: '', login: '', state: 'connection_required', write_performed: false });
  vi.mocked(api.markGitHubStarSupported).mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('plugin invitations use immediate support only', () => {
  it('uses local input and the existing claim, then stars a saved account only after confirmation', async () => {
    const view = render(host(null));
    await act(async () => window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: opportunity })));
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    vi.mocked(api.readDeferredGitHubStarIntent).mockResolvedValue({ repository, local_supported: true, can_receive: false, accounts: [account], intent: null });
    await act(async () => view.rerender(host()));
    expect(screen.getByRole('dialog', { name: '感谢使用 LazyMind' })).toBeInTheDocument();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledWith(opportunity);
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    await confirm();
    expect(api.writeGitHubStar).toHaveBeenCalledWith({ connection_id: account.connection_id, expected_account_id: account.account_id, confirmed: true });
    expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('status')).toHaveTextContent('感谢支持，已点 Star');
    expect(window.open).not.toHaveBeenCalled(); expectNoDeferredWrite();
  });

  it('uses the current local credential immediately when there is no usable saved account', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [{ ...account, state: 'unknown', eligible: false }], can_star: true });
    vi.mocked(api.writeLocalGitHubStar).mockResolvedValue({ repository, account_id: '20002', login: 'bob', state: 'starred', write_performed: true });
    await offer(); await confirm();
    expect(api.writeLocalGitHubStar).toHaveBeenCalledWith({ confirmed: true });
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    expect(screen.getByRole('status')).toHaveTextContent('感谢支持，已点 Star');
    expect(popup.close).toHaveBeenCalledTimes(1); expect(popup.location.replace).not.toHaveBeenCalled();
    expectNoDeferredWrite();
  });

  it('opens the fixed repository outside the iframe when no credential is available and stops inviting', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [] });
    const view = await offer(); await confirm();
    expect(window.open).toHaveBeenCalledWith('about:blank', '_blank');
    expect(popup.opener).toBeNull(); expect(popup.location.replace).toHaveBeenCalledWith(repositoryURL);
    expect(popup.close).not.toHaveBeenCalled();
    expect(screen.getByRole('link', { name: '打开 GitHub 项目页面' })).toHaveAttribute('href', repositoryURL);
    expect(screen.getByRole('link')).toHaveAttribute('target', '_blank');
    expect(screen.queryByText('感谢支持，已点 Star')).not.toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '关闭' })));
    await act(async () => view.rerender(host({ ...opportunity, task_id: 'root-4' })));
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1); expectNoDeferredWrite();
  });

  it('keeps a manual repository link when popups are blocked and never writes without model permission', async () => {
    vi.mocked(window.open).mockReturnValue(null);
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [], can_star: false });
    await offer(); await confirm();
    expect(window.open).toHaveBeenCalledWith(repositoryURL, '_blank', 'noopener,noreferrer');
    expect(screen.getByRole('link', { name: '打开 GitHub 项目页面' })).toHaveAttribute('rel', 'noopener noreferrer');
    expect(api.writeGitHubStar).not.toHaveBeenCalled(); expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    expectNoDeferredWrite();
  });

  it('remembers confirmed support across plugin and LazyMind remounts even when saving the Core preference fails', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [] });
    vi.mocked(api.markGitHubStarSupported).mockRejectedValue({ response: { status: 503 } });
    const first = await offer(); await confirm();
    expect(screen.getByRole('link', { name: '打开 GitHub 项目页面' })).toBeInTheDocument();
    first.unmount();

    const next = { ...opportunity, task_id: 'root-after-cooldown' };
    vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(next);
    vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({ show: true, reason: 'eligible', task_id: next.task_id,
      min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 });
    const second = render(host(next));
    await act(async () => {});
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    second.unmount();

    render(<MemoryRouter><ConfigProvider theme={{ token: { motion: false } }}>
      <GitHubStarPrompt userId={identity.owner} blocked={false} returnTo='/task-center' />
    </ConfigProvider></MemoryRouter>);
    await act(async () => window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: next })));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    expect(api.writeLocalGitHubStar).toHaveBeenCalledTimes(1);
    expectNoDeferredWrite();
  });

  it('suppresses invitations for a previous exhausted support intent without consuming quota', async () => {
    vi.mocked(api.readDeferredGitHubStarIntent).mockResolvedValue({ repository, local_supported: true, can_receive: true, accounts: [], intent: {
      intent_id: 'past-support', status: 'exhausted', source: 'local', connection_id: null, account_id: null,
      created_at: '2026-11-01T00:00:00Z', expires_at: '2026-11-08T00:00:00Z', attempt_count: 28,
      next_attempt_at: null, rate_limit_not_before: null, reason: 'expired', remote_state: 'unknown',
    } });
    await offer();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled(); expect(api.readGitHubStarAccounts).not.toHaveBeenCalled();
    expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1); expectNoDeferredWrite();
  });

  it('cannot synchronize an old owner marker after another window replaces the login', async () => {
    publishGitHubStarSupport('owner-1', true);
    const view = render(host(null));
    // Another window updates shared login storage without this window's custom auth event.
    const notice = new StorageEvent('storage', {
      key: api.GITHUB_STAR_SUPPORT_ACCEPTED_STORAGE_KEY,
      newValue: localStorage.getItem(api.GITHUB_STAR_SUPPORT_ACCEPTED_STORAGE_KEY),
    });
    Object.defineProperty(notice, 'storageArea', { value: localStorage });
    identity.session = 'owner-1:replacement-session';
    await act(async () => window.dispatchEvent(notice));
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    identity.authOwner = 'owner-2'; identity.session = 'owner-2:session-1';
    await act(async () => window.dispatchEvent(notice));
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    view.unmount();

    // A remount with stale owner props must not treat B's freshly read identity as A's.
    render(host());
    await act(async () => {});
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(api.writeGitHubStar).not.toHaveBeenCalled(); expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    expectNoDeferredWrite();
  });

  it('cannot navigate or update a new owner after an old local request completes', async () => {
    let finish!: (value: api.LocalGitHubStarResult) => void;
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [] });
    vi.mocked(api.writeLocalGitHubStar).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const view = await offer(); await confirm();
    identity.owner = 'owner-2'; identity.authOwner = 'owner-2'; identity.session = 'owner-2:session-1';
    await act(async () => view.rerender(host(null)));
    await act(async () => finish({ repository, account_id: '', login: '', state: 'connection_required', write_performed: false }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expect(popup.location.replace).not.toHaveBeenCalled();
    expect(popup.close).toHaveBeenCalledTimes(1);
    expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1); expectNoDeferredWrite();
  });

  it('reports an uncertain immediate result without retrying, queueing or claiming Star success', async () => {
    vi.mocked(api.writeGitHubStar).mockRejectedValue({ response: { data: { reason: 'result_unknown' } } });
    await offer(); await confirm();
    expect(screen.getByRole('alert')).toHaveTextContent('暂未确认是否成功');
    expect(screen.getByRole('link', { name: '打开 GitHub 项目页面' })).toHaveAttribute('href', repositoryURL);
    expect(screen.queryByRole('button', { name: '去 GitHub 点 Star' })).not.toBeInTheDocument();
    expect(screen.queryByText('感谢支持，已点 Star')).not.toBeInTheDocument();
    expect(api.writeGitHubStar).toHaveBeenCalledTimes(1); expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1);
    expectNoDeferredWrite();
  });
});
