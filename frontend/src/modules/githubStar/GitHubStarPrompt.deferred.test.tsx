import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BASE_URL } from '@/components/request';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as api from './api';

const mocks = vi.hoisted(() => ({
  readDeferredGitHubStarIntent: vi.fn(), acceptDeferredGitHubStarIntent: vi.fn(), cancelDeferredGitHubStarIntent: vi.fn(),
  navigate: vi.fn(), identity: { owner: 'owner-1', session: 'owner-1:session-1' }, route: { state: null as unknown },
}));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(),
  readDeferredGitHubStarIntent: mocks.readDeferredGitHubStarIntent,
  acceptDeferredGitHubStarIntent: mocks.acceptDeferredGitHubStarIntent,
  cancelDeferredGitHubStarIntent: mocks.cancelDeferredGitHubStarIntent,
  readGitHubStarAccounts: vi.fn(), readLocalGitHubStarCapability: vi.fn(), claimGitHubStarPrompt: vi.fn(),
  findGitHubStarOpportunity: vi.fn(), writeGitHubStar: vi.fn(), writeLocalGitHubStar: vi.fn(), markGitHubStarSupported: vi.fn(),
}));
vi.mock('@/components/auth', () => ({
  AgentAppsAuth: { getUserInfo: () => ({ userId: mocks.identity.owner }), getSessionIdentity: () => mocks.identity.session,
    getAuthHeaders: () => ({}) }, AUTH_USER_CHANGE_EVENT: 'fixture-user-change', AUTH_LOGOUT_EVENT: 'fixture-logout',
}));
vi.mock('react-router-dom', async original => ({
  ...await original<typeof import('react-router-dom')>(), useNavigate: () => mocks.navigate,
  useLocation: () => ({ pathname: '/task-center', search: '', hash: '', key: 'fixture', state: mocks.route.state }),
}));
vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => ({
    title: '支持 LazyMind', message: '觉得 LazyMind 对你有帮助吗？欢迎支持项目。',
    star: '为项目点 Star', close: '关闭', account: 'GitHub 账号', selectAccount: '选择 GitHub 账号',
    thankYou: '感谢您的支持！', supportProject: '支持项目', cancelSupportRequest: '取消本次支持请求',
    success: '感谢支持，已点 Star', alreadyStarred: '你已为项目点过 Star，感谢支持',
    failed: '未能点 Star，请重试', unknownResult: '暂未确认是否成功，请稍后查询',
    retry: '重试', accountChanged: 'GitHub 账号已变化，请重新确认', continueAuth: '继续连接 GitHub',
  } as Record<string, string>)[key.split('.').pop()!] || key }),
}));

type HistoryStatus = 'pending' | 'needs_attention' | 'succeeded' | 'cancelled' | 'exhausted';
const repository = 'LazyAGI/LazyMind';
const acceptedEvent = 'lazymind:github-star-support-accepted';
const opportunity: api.GitHubStarOpportunity = {
  task_id: 'root-3', conversation_id: 'work-conversation', status: 'succeeded', result_ready: true, source: 'final_result',
};
const savedAccount = { connection_id: 'conn-alice', account_id: '10001', login: 'alice' };
const claim: api.GitHubStarClaim = {
  show: true, reason: 'eligible', task_id: opportunity.task_id, min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
};
function history(status: HistoryStatus = 'pending') {
  return { intent_id: 'intent-owner-1', status, source: 'local' as const, connection_id: null, account_id: null,
    created_at: '2026-11-17T00:00:00Z', expires_at: '2026-11-24T00:00:00Z', attempt_count: 1,
    next_attempt_at: status === 'pending' ? '2026-11-17T06:00:00Z' : null, rate_limit_not_before: null,
    reason: status === 'needs_attention' ? 'binding_not_captured' : '',
    remote_state: status === 'succeeded' ? 'starred' as const : 'unknown' as const };
}
let response: {
  repository: string; can_receive: boolean; local_supported: boolean; accounts: typeof savedAccount[];
  intent: ReturnType<typeof history> | null;
};

function host(userId = mocks.identity.owner) {
  return <ConfigProvider theme={{ token: { motion: false } }}>
    <GitHubStarPrompt userId={userId} blocked={false} returnTo='/task-center' />
  </ConfigProvider>;
}
async function announce() {
  await act(async () => {
    window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: opportunity }));
  });
}
async function offer() {
  render(host());
  await announce();
  return screen.getByRole('dialog', { name: '支持 LazyMind' });
}
function latch<T>() {
  let resolve!: (value: T) => void;
  return { promise: new Promise<T>(finish => { resolve = finish; }), resolve: (value: T) => resolve(value) };
}
function notification(owner = 'owner-1', scope = BASE_URL || window.location.origin) {
  return { service_scope: scope, owner_user_id: owner, notice_id: 'fixture-notice-1' };
}
async function notify(kind: 'window' | 'storage', payload = notification()) {
  await act(async () => {
    if (kind === 'window') {
      window.dispatchEvent(new CustomEvent(acceptedEvent, { detail: payload }));
      return;
    }
    const event = new StorageEvent('storage', { key: acceptedEvent, newValue: JSON.stringify(payload) });
    // vitest.setup replaces Storage with a memory object; jsdom's constructor
    // rejects that object even though listeners must observe the same storage.
    Object.defineProperty(event, 'storageArea', { value: localStorage });
    window.dispatchEvent(event);
  });
}
function expectNoStarWrite() {
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
  expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
}
function expectOnlyThanks() {
  expect(screen.getByText('感谢您的支持！')).toBeInTheDocument();
  expect(screen.queryByText(/已点\s*Star|已为项目点过\s*Star|排队|网络恢复|稍后自动|延迟执行/)).not.toBeInTheDocument();
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date('2026-11-17T00:00:00Z'));
  mocks.identity.owner = 'owner-1'; mocks.identity.session = 'owner-1:session-1'; mocks.route.state = null;
  sessionStorage.clear(); localStorage.clear();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  response = { repository, can_receive: true, local_supported: true, accounts: [], intent: null };
  mocks.readDeferredGitHubStarIntent.mockImplementation(async () => ({ ...response }));
  mocks.acceptDeferredGitHubStarIntent.mockImplementation(async () => {
    response = { ...response, intent: history() };
    return { repository, accepted: true, intent: response.intent };
  });
  mocks.cancelDeferredGitHubStarIntent.mockResolvedValue({ repository, intent: history('cancelled') });
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [], can_star: true });
  vi.mocked(api.readLocalGitHubStarCapability).mockResolvedValue({ repository, eligible: false });
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue(claim);
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  vi.mocked(api.markGitHubStarSupported).mockResolvedValue(undefined);
  vi.mocked(api.writeLocalGitHubStar).mockRejectedValue(new Error('fixture GitHub unreachable'));
  vi.mocked(api.writeGitHubStar).mockRejectedValue(new Error('fixture GitHub unreachable'));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

describe('persistent support confirmation through the real invitation', () => {
  it('binds the selected saved identity from offline metadata without first attempting an immediate Star', async () => {
    response.accounts = [savedAccount];
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository, accounts: [{ ...savedAccount, state: 'unknown', eligible: false }] });
    await offer();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledWith({ confirmed: true, source: 'connection',
      connection_id: savedAccount.connection_id, expected_account_id: savedAccount.account_id });
    expectOnlyThanks(); expectNoStarWrite();
  });

  it('requires a choice between saved identities before accepting a connection-bound intent', async () => {
    response.accounts = [savedAccount, { connection_id: 'conn-bob', account_id: '20002', login: 'bob' }];
    await offer();
    const button = screen.getByRole('button', { name: '为项目点 Star' });
    await act(async () => fireEvent.click(button));
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled();
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'GitHub 账号' }));
    await act(async () => fireEvent.click(screen.getByText('bob')));
    await act(async () => fireEvent.click(button));
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledWith({ confirmed: true, source: 'connection',
      connection_id: 'conn-bob', expected_account_id: '20002' });
    expectOnlyThanks(); expectNoStarWrite();
  });

  it('waits for persistent acceptance and prevents duplicate clicks before showing thanks', async () => {
    const accepted = latch<{ repository: string; accepted: true; intent: ReturnType<typeof history> }>();
    mocks.acceptDeferredGitHubStarIntent.mockReturnValue(accepted.promise);
    await offer();
    const button = screen.getByRole('button', { name: '为项目点 Star' });
    await act(async () => { fireEvent.click(button); fireEvent.click(button); });
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('感谢您的支持！')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    await act(async () => accepted.resolve({ repository, accepted: true, intent: history('needs_attention') }));
    expectOnlyThanks(); expectNoStarWrite();
  });

  it('does not thank or mark support when persistence rejects the confirmation', async () => {
    mocks.acceptDeferredGitHubStarIntent.mockRejectedValue(new Error('fixture local storage unavailable'));
    await offer();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('感谢您的支持！')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    expectNoStarWrite();
  });

  it('closing before confirmation neither records support nor changes the support preference', async () => {
    await offer();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '关闭' })));
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(mocks.cancelDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    expectNoStarWrite();
  });

  it('keeps thanks and suppresses another component even when Core preference synchronization fails', async () => {
    vi.mocked(api.markGitHubStarSupported).mockRejectedValue(new Error('fixture Core unavailable'));
    render(<>{host()}{host()}</>);
    await announce();
    const dialogs = screen.getAllByRole('dialog', { name: '支持 LazyMind' });
    expect(dialogs).toHaveLength(2);
    await act(async () => fireEvent.click(within(dialogs[0]).getByRole('button', { name: '为项目点 Star' })));
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    expect(screen.getAllByRole('dialog', { name: '支持 LazyMind' })).toHaveLength(1);
    expectOnlyThanks();
    expect(screen.queryByRole('button', { name: '为项目点 Star' })).not.toBeInTheDocument();
    expectNoStarWrite();
  });

  it('accepting in one component prevents another component from displaying its late claim after Core fails', async () => {
    const oldClaim = latch<api.GitHubStarClaim>();
    vi.mocked(api.claimGitHubStarPrompt).mockResolvedValueOnce(claim).mockReturnValueOnce(oldClaim.promise);
    vi.mocked(api.markGitHubStarSupported).mockRejectedValue(new Error('fixture Core unavailable'));
    render(<>{host()}{host()}</>); await announce();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(2);
    expect(screen.getAllByRole('dialog', { name: '支持 LazyMind' })).toHaveLength(1);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    await act(async () => oldClaim.resolve(claim));
    expect(screen.getAllByRole('dialog', { name: '支持 LazyMind' })).toHaveLength(1);
    expectOnlyThanks();
    expect(screen.queryByRole('button', { name: '为项目点 Star' })).not.toBeInTheDocument(); expectNoStarWrite();
  });

  it('does not use a late acceptance for a previous session as the new owner support preference', async () => {
    const accepted = latch<{ repository: string; accepted: true; intent: ReturnType<typeof history> }>();
    mocks.acceptDeferredGitHubStarIntent.mockReturnValue(accepted.promise);
    const view = render(host()); await announce();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
    mocks.identity.owner = 'owner-2'; mocks.identity.session = 'owner-2:session-1';
    await act(async () => view.rerender(host()));
    await act(async () => accepted.resolve({ repository, accepted: true, intent: history() }));
    expect(screen.queryByText('感谢您的支持！')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    await announce();
    expect(screen.getByRole('dialog', { name: '支持 LazyMind' })).toBeInTheDocument(); expectNoStarWrite();
  });
});

describe('authoritative support history stops every automatic invitation', () => {
  it.each<HistoryStatus>(['pending', 'needs_attention', 'succeeded', 'cancelled', 'exhausted'])(
    '%s remains suppressive after seven days and a failed Core synchronization', async status => {
      response.intent = history(status);
      vi.setSystemTime(new Date('2026-11-25T00:00:00Z'));
      vi.mocked(api.markGitHubStarSupported).mockRejectedValue(new Error('fixture Core unavailable'));
      render(host()); await announce();
      expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalled();
      expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(api.markGitHubStarSupported).toHaveBeenCalled();
      expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoStarWrite();
    });

  it.each(['work completion', 'authentication return'])('does not treat failed history lookup as permission to show: %s', async entry => {
    mocks.readDeferredGitHubStarIntent.mockRejectedValue(new Error('fixture history unavailable'));
    if (entry === 'authentication return') mocks.route.state = { githubStarResume: {
      userId: 'owner-1', connectionId: savedAccount.connection_id, opportunity,
    } };
    render(host());
    if (entry === 'work completion') await announce(); else await act(async () => {});
    expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expectNoStarWrite();
  });

  it('checks history before restoring an authentication return invitation', async () => {
    response.intent = history('cancelled');
    mocks.route.state = { githubStarResume: { userId: 'owner-1', connectionId: savedAccount.connection_id, opportunity } };
    render(host()); await act(async () => {});
    expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled(); expectNoStarWrite();
  });

  it('rechecks history after a preview-delayed opportunity instead of reviving its old permission', async () => {
    vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList);
    const preview = document.createElement('div'); preview.className = 'artifact-preview'; document.body.append(preview);
    try {
      render(host()); await announce();
      expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
      response.intent = history('exhausted');
      await act(async () => preview.remove());
      expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalled();
      expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expectNoStarWrite();
    } finally { preview.remove(); }
  });

  it.each(['claim', 'post-claim task lookup'])('rechecks durable history after a late %s response even with no notification', async gate => {
    const claimed = latch<api.GitHubStarClaim>();
    const task = latch<api.GitHubStarOpportunity | null>();
    if (gate === 'claim') vi.mocked(api.claimGitHubStarPrompt).mockReturnValue(claimed.promise);
    else vi.mocked(api.findGitHubStarOpportunity).mockResolvedValueOnce(opportunity).mockReturnValue(task.promise);
    render(host()); await announce();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    response.intent = history('needs_attention');
    await act(async () => { if (gate === 'claim') claimed.resolve(claim); else task.resolve(opportunity); });
    expect(mocks.readDeferredGitHubStarIntent.mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expectNoStarWrite();
  });

  it('fails closed when the final history recheck fails after the claim', async () => {
    const claimed = latch<api.GitHubStarClaim>();
    vi.mocked(api.claimGitHubStarPrompt).mockReturnValue(claimed.promise);
    render(host()); await announce();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    mocks.readDeferredGitHubStarIntent.mockRejectedValue(new Error('fixture final history check failed'));
    await act(async () => claimed.resolve(claim));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument(); expectNoStarWrite();
  });

  it('a closed and remounted invitation still observes accepted history after Core failed', async () => {
    vi.mocked(api.markGitHubStarSupported).mockRejectedValue(new Error('fixture Core unavailable'));
    const view = render(host()); await announce();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expectOnlyThanks();
    view.unmount();
    vi.mocked(api.claimGitHubStarPrompt).mockClear();
    render(host()); await announce();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(mocks.acceptDeferredGitHubStarIntent).toHaveBeenCalledTimes(1);
  });
});

describe('support acceptance notifications invalidate invitations without granting permissions', () => {
  it.each(['window', 'storage'] as const)('%s notification closes an already visible invitation but cannot itself write', async kind => {
    await offer();
    await notify(kind);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(mocks.readDeferredGitHubStarIntent).toHaveBeenCalled();
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(mocks.cancelDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled(); expectNoStarWrite();
  });

  it.each(['window', 'storage'] as const)('%s notification invalidates an in-flight old claim even before history is visible', async kind => {
    const claimed = latch<api.GitHubStarClaim>();
    vi.mocked(api.claimGitHubStarPrompt).mockReturnValue(claimed.promise);
    render(host()); await announce();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    await notify(kind);
    await act(async () => claimed.resolve(claim));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled(); expectNoStarWrite();
  });

  it.each(['other owner', 'other service'])('does not apply a %s storage notice to the current invitation', async foreign => {
    await offer();
    await notify('storage', foreign === 'other owner' ? notification('owner-2') : notification('owner-1', 'https://other-service.test'));
    expect(screen.getByRole('dialog', { name: '支持 LazyMind' })).toBeInTheDocument();
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled(); expectNoStarWrite();
  });

  it('an old owner notice and a late old response cannot suppress or act for a newly logged-in owner', async () => {
    const claimed = latch<api.GitHubStarClaim>();
    vi.mocked(api.claimGitHubStarPrompt).mockReturnValueOnce(claimed.promise).mockResolvedValue(claim);
    const view = render(host()); await announce();
    mocks.identity.owner = 'owner-2'; mocks.identity.session = 'owner-2:session-1';
    await act(async () => view.rerender(host()));
    await notify('storage', notification('owner-1'));
    await act(async () => claimed.resolve(claim));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await announce();
    expect(screen.getByRole('dialog', { name: '支持 LazyMind' })).toBeInTheDocument();
    expect(mocks.acceptDeferredGitHubStarIntent).not.toHaveBeenCalled();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled(); expectNoStarWrite();
  });
});
