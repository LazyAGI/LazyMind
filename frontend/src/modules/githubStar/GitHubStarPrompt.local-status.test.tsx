import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BASE_URL } from '@/components/request';
import i18n from '@/i18n';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as api from './api';

const { identity, navigate } = vi.hoisted(() => ({
  identity: { owner: 'owner-1', session: 'owner-1:session-1' }, navigate: vi.fn(),
}));
vi.mock('@/components/auth', () => ({
  AgentAppsAuth: { getUserInfo: () => ({ userId: identity.owner }), getSessionIdentity: () => identity.session,
    getAuthHeaders: () => ({}) }, AUTH_USER_CHANGE_EVENT: 'fixture-user-change', AUTH_LOGOUT_EVENT: 'fixture-logout',
}));
vi.mock('react-router-dom', async original => ({
  ...await original<typeof import('react-router-dom')>(), useNavigate: () => navigate,
}));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(), readDeferredGitHubStarIntent: vi.fn(), acceptDeferredGitHubStarIntent: vi.fn(),
  readGitHubStarAccounts: vi.fn(), readLocalGitHubStarCapability: vi.fn(), findGitHubStarOpportunity: vi.fn(),
  claimGitHubStarPrompt: vi.fn(), markGitHubStarSupported: vi.fn(), writeGitHubStar: vi.fn(), writeLocalGitHubStar: vi.fn(),
}));

const repository = 'LazyAGI/LazyMind';
const account = { connection_id: 'saved-alice', account_id: '10001', login: 'alice' };
const opportunity: api.GitHubStarOpportunity = { task_id: 'new-root', conversation_id: 'work-1',
  status: 'succeeded', result_ready: true, source: 'final_result' };
const intent: api.DeferredGitHubStarIntent = {
  intent_id: 'support-owner-1', status: 'pending', source: 'connection', connection_id: account.connection_id,
  account_id: account.account_id, created_at: '2026-11-17T00:00:00Z', expires_at: '2026-11-24T00:00:00Z',
  attempt_count: 0, next_attempt_at: '2026-11-17T00:00:00Z', rate_limit_not_before: null, reason: '', remote_state: 'unknown',
};
let support: api.DeferredGitHubStarStatus;
function observation(state: api.GitHubStarAccount['state']) {
  return { repository, can_star: true, accounts: [{ ...account, state, eligible: state !== 'unknown' }] };
}
function host() {
  return <MemoryRouter><ConfigProvider theme={{ token: { motion: false } }}>
    <GitHubStarPrompt userId={identity.owner} blocked={false} returnTo='/task-center' />
  </ConfigProvider></MemoryRouter>;
}
async function announce() {
  await act(async () => window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: opportunity })));
}
function expectNoImmediateWriteOrHostProbe() {
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
  expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
  expect(api.readLocalGitHubStarCapability).not.toHaveBeenCalled();
}

beforeEach(async () => {
  vi.resetAllMocks(); localStorage.clear(); sessionStorage.clear();
  identity.owner = 'owner-1'; identity.session = 'owner-1:session-1';
  await i18n.changeLanguage('zh-CN');
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  support = { repository, can_receive: true, local_supported: true, accounts: [account], intent: null };
  vi.mocked(api.readDeferredGitHubStarIntent).mockImplementation(async () => ({ ...support }));
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue(observation('unknown'));
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({ show: true, reason: 'eligible', task_id: opportunity.task_id,
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 });
  vi.mocked(api.markGitHubStarSupported).mockResolvedValue(undefined);
  vi.mocked(api.acceptDeferredGitHubStarIntent).mockImplementation(async body => {
    const accepted = body.source === 'connection' ? intent : { ...intent, source: 'local' as const, connection_id: null, account_id: null };
    support = { ...support, intent: accepted };
    return { repository, accepted: true, intent: accepted };
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('local invitations preserve observed saved-account Star status', () => {
  it('does not claim or invite an already-Starred local saved account with no prior support history', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue(observation('starred'));
    render(host()); await announce();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(1);
    expect(api.markGitHubStarSupported).toHaveBeenCalled();
    expect(api.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrHostProbe();
  });

  it('falls back to offline account metadata when the saved-account observation fails', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockRejectedValue(new Error('fixture GitHub unreachable'));
    render(host()); await announce();
    expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: '为项目点 Star' })).toBeEnabled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(api.acceptDeferredGitHubStarIntent).toHaveBeenCalledWith({ confirmed: true, source: 'connection',
      connection_id: account.connection_id, expected_account_id: account.account_id });
    expect(screen.getByText('感谢您的支持！')).toBeInTheDocument();
    expect(screen.queryByText('感谢支持，已点 Star')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '继续连接 GitHub' })).not.toBeInTheDocument();
    expectNoImmediateWriteOrHostProbe();
  });

  it('accepts local support only on explicit confirmation without observing absent saved connections', async () => {
    support.accounts = [];
    render(host()); await announce();
    expect(api.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrHostProbe();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(api.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(api.acceptDeferredGitHubStarIntent).toHaveBeenCalledWith({ source: 'local', confirmed: true });
    expect(api.readGitHubStarAccounts).not.toHaveBeenCalled(); expectNoImmediateWriteOrHostProbe();
    expect(screen.getByText('感谢您的支持！')).toBeInTheDocument();
    expect(screen.queryByText(/已点\s*Star|已为项目点过\s*Star|排队|网络恢复|稍后自动|延迟执行/)).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('cannot use a delayed Star observation to mark support for a new owner', async () => {
    let finish!: (value: ReturnType<typeof observation>) => void;
    vi.mocked(api.readGitHubStarAccounts).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const view = render(host()); await announce();
    expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(1);
    identity.owner = 'owner-2'; identity.session = 'owner-2:session-1';
    await act(async () => view.rerender(host()));
    await act(async () => finish(observation('starred')));
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expectNoImmediateWriteOrHostProbe();
  });

  it('cannot reopen an invitation after support was accepted while its Star observation was pending', async () => {
    let finish!: (value: ReturnType<typeof observation>) => void;
    vi.mocked(api.readGitHubStarAccounts).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    render(host()); await announce();
    expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(1);
    support.intent = intent;
    await act(async () => window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_SUPPORT_ACCEPTED_EVENT, { detail: {
      service_scope: BASE_URL || window.location.origin, owner_user_id: identity.owner, notice_id: 'saved-account-query-notice',
    } })));
    await act(async () => finish(observation('not_starred')));
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoImmediateWriteOrHostProbe();
  });
});
