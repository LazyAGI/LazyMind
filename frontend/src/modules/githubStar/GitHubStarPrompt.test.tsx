import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as api from './api';

const { navigate, route } = vi.hoisted(() => ({ navigate: vi.fn(), route: { state: null as unknown } }));
vi.mock('react-router-dom', async original => ({
  ...await original<typeof import('react-router-dom')>(), useNavigate: () => navigate,
  useLocation: () => ({ pathname: '/task-center', search: '', hash: '', key: 'fixture', state: route.state }),
}));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(),
  readGitHubStarAccounts: vi.fn(), claimGitHubStarPrompt: vi.fn(),
  writeGitHubStar: vi.fn(), writeLocalGitHubStar: vi.fn(), markGitHubStarSupported: vi.fn(), findGitHubStarOpportunity: vi.fn(),
}));
vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => ({
  'githubStar.title': '支持 LazyMind',
  'githubStar.message': '觉得 LazyMind 对你有帮助吗？欢迎为我们的 GitHub 项目点个 Star，支持我们持续改进。',
  'githubStar.star': '为项目点 Star', 'githubStar.close': '关闭', 'common.close': '关闭',
  'githubStar.account': 'GitHub 账号', 'githubStar.selectAccount': '选择 GitHub 账号',
  'githubStar.localHint': '未找到可用的本机 GitHub 登录信息，请连接账号后继续。',
  'githubStar.continueAuth': '继续连接 GitHub', 'githubStar.success': '感谢支持，已点 Star',
  'githubStar.alreadyStarred': '你已为项目点过 Star，感谢支持',
  'githubStar.permissionDenied': 'GitHub 权限不足，请重新授权',
  'githubStar.unknownResult': '暂未确认是否成功，请稍后查询',
  'githubStar.failed': '未能点 Star，请重试', 'githubStar.accountChanged': 'GitHub 账号已变化，请重新确认',
  'githubStar.rateLimited': '请求过于频繁，请稍后重试',
} as Record<string, string>)[key] || key }) }));

const opportunity: api.GitHubStarOpportunity = {
  task_id: 'root-3', conversation_id: 'work-conversation', status: 'succeeded',
  result_ready: true, source: 'final_result',
};
const account: api.GitHubStarAccount = {
  connection_id: 'conn-alice', account_id: '10001', login: 'alice', state: 'not_starred', eligible: true,
};
const success: api.GitHubStarResult = {
  repository: 'LazyAGI/LazyMind', connection_id: account.connection_id, account_id: account.account_id,
  login: account.login, state: 'starred', write_performed: true,
};
const localSuccess = {
  repository: 'LazyAGI/LazyMind', account_id: '30003', login: 'local-user', state: 'starred' as const, write_performed: true,
};

async function announce(value: api.GitHubStarOpportunity = opportunity) {
  await act(async () => {
    window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: value }));
  });
}

function host(props: Partial<Parameters<typeof GitHubStarPrompt>[0]> = {}) {
  return <ConfigProvider theme={{ token: { motion: false } }}>
    <GitHubStarPrompt userId='owner-1' blocked={false} returnTo='/task-center' {...props} />
  </ConfigProvider>;
}

function mount(props: Partial<Parameters<typeof GitHubStarPrompt>[0]> = {}) {
  return render(host(props));
}

async function offer() {
  mount();
  await announce();
  return screen.getByRole('dialog', { name: '支持 LazyMind' });
}

beforeEach(() => {
  vi.clearAllMocks();
  route.state = null;
  sessionStorage.clear();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [account] });
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({
    show: true, reason: 'eligible', task_id: opportunity.task_id,
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
  });
  vi.mocked(api.writeGitHubStar).mockResolvedValue(success);
  vi.mocked(api.writeLocalGitHubStar).mockResolvedValue({
    repository: 'LazyAGI/LazyMind', state: 'connection_required', account_id: '', login: '', write_performed: false,
  });
  vi.mocked(api.markGitHubStarSupported).mockResolvedValue(undefined);
  vi.mocked(api.findGitHubStarOpportunity).mockImplementation(async (conversationId, workflowSessionId, taskIds) => {
    if (conversationId !== opportunity.conversation_id || workflowSessionId !== undefined) return null;
    const taskId = taskIds?.find(id => ['root-3', 'root-4'].includes(id));
    return taskId ? { ...opportunity, task_id: taskId } : null;
  });
});
afterEach(cleanup);

describe('GitHub Star invitation', () => {
  it('does not prompt or request a claim merely on first mount or task completion', async () => {
    mount();
    await act(async () => {});
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
  });

  it.each(['running', 'pending', 'waiting', 'failed', 'canceled', 'paused'])('does not invite for %s work', async status => {
    mount();
    await announce({ ...opportunity, status });
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it.each([
    { ...opportunity, result_ready: false },
    { ...opportunity, source: 'stage' as const },
  ])('does not invite without a reviewed final result: %j', async value => {
    mount();
    await announce(value);
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it.each(['busy', 'hidden'])('does not consume an invitation while %s', async situation => {
    mount({ blocked: situation === 'busy' });
    if (situation === 'hidden') Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    await announce();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('offers only after a reviewed successful result and uses the server claim', async () => {
    await offer();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledWith(opportunity);
    expect(screen.getByText('觉得 LazyMind 对你有帮助吗？欢迎为我们的 GitHub 项目点个 Star，支持我们持续改进。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '为项目点 Star' })).toBeInTheDocument();
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('honors denied server frequency eligibility', async () => {
    vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({
      show: false, reason: 'cooldown', task_id: opportunity.task_id,
      min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
    });
    mount();
    await announce();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('known Star support suppresses the invitation without consuming quota', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({
      repository: 'LazyAGI/LazyMind', accounts: [{ ...account, state: 'starred' }],
    });
    mount();
    await announce();
    expect(api.readGitHubStarAccounts).toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('tries local credentials only on click when all saved accounts are unavailable', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({
      repository: 'LazyAGI/LazyMind', accounts: [{ ...account, state: 'unknown', eligible: false }],
    });
    vi.mocked(api.writeLocalGitHubStar).mockResolvedValue(localSuccess);
    await offer();
    expect(api.readGitHubStarAccounts).toHaveBeenCalled();
    expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(api.writeLocalGitHubStar).toHaveBeenCalledWith({ confirmed: true });
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    expect(screen.getByText('感谢支持，已点 Star')).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('a failed read-only check does not consume quota or launch authentication', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockRejectedValue(new Error('fixture network failure'));
    mount();
    await announce();
    expect(api.readGitHubStarAccounts).toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('a connected account stars only after the explicit button click without reauthentication', async () => {
    await offer();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(api.writeGitHubStar).toHaveBeenCalledTimes(1);
    expect(api.writeGitHubStar).toHaveBeenCalledWith({
      connection_id: 'conn-alice', expected_account_id: '10001', confirmed: true,
    });
    expect(screen.getByText('感谢支持，已点 Star')).toBeInTheDocument();
    expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1);
    expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each(['saved', 'none'])('closing the invitation with %s accounts does not write, authenticate, or consume another claim', async kind => {
    if (kind === 'none') vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [] });
    await offer();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '关闭' })));
    await announce({ ...opportunity, task_id: 'root-4' });
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByText('不好用')).not.toBeInTheDocument();
    expect(screen.queryByText('取消 Star')).not.toBeInTheDocument();
  });

  it('several connected accounts require an explicit account selection', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [
      account, { ...account, connection_id: 'conn-bob', account_id: '20002', login: 'bob' },
    ] });
    await offer();
    const button = screen.getByRole('button', { name: '为项目点 Star' });
    fireEvent.click(button);
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'GitHub 账号' }));
    fireEvent.click(screen.getByText('bob'));
    await act(async () => fireEvent.click(button));
    expect(api.writeGitHubStar).toHaveBeenCalledWith({ connection_id: 'conn-bob', expected_account_id: '20002', confirmed: true });
  });

  it('offers connection only after local credentials are confirmed unavailable', async () => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [] });
    await offer();
    expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(api.writeLocalGitHubStar).toHaveBeenCalledTimes(1);
    expect(api.writeLocalGitHubStar).toHaveBeenCalledWith({ confirmed: true });
    expect(screen.getByText(/未找到可用的本机 GitHub 登录信息/)).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '继续连接 GitHub' })));
    expect(navigate).toHaveBeenCalledWith('/cloud-documents/github', {
      state: { githubStarReturn: { userId: 'owner-1', returnTo: '/task-center', opportunity } },
    });
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
  });

  it.each([true, false])('uses local credentials directly without connecting: write_performed=%s', async writePerformed => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [] });
    vi.mocked(api.writeLocalGitHubStar).mockResolvedValue({ ...localSuccess, write_performed: writePerformed });
    await offer();
    expect(api.writeLocalGitHubStar).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(api.writeLocalGitHubStar).toHaveBeenCalledWith({ confirmed: true });
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    expect(screen.getByText(writePerformed ? '感谢支持，已点 Star' : '你已为项目点过 Star，感谢支持')).toBeInTheDocument();
    expect(api.markGitHubStarSupported).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: '继续连接 GitHub' })).not.toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each([
    [503, 'unavailable', /重试/], [503, 'result_unknown', /暂未确认是否成功/],
    [429, 'rate_limited', /请求过于频繁/], [403, 'permission_denied', /权限不足/],
  ])('local failure %s/%s does not become a missing-credential result', async (status, reason, message) => {
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [] });
    vi.mocked(api.writeLocalGitHubStar).mockRejectedValue({ response: { status, data: { reason } } });
    await offer();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(screen.getByRole('alert')).toHaveTextContent(message);
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: '继续连接 GitHub' })).not.toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('does not replay a pending local write after double clicking', async () => {
    let finish!: (result: typeof localSuccess) => void;
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [] });
    vi.mocked(api.writeLocalGitHubStar).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    await offer();
    const button = screen.getByRole('button', { name: '为项目点 Star' });
    fireEvent.click(button); fireEvent.click(button);
    expect(api.writeLocalGitHubStar).toHaveBeenCalledTimes(1);
    await act(async () => finish(localSuccess));
    expect(screen.getByText('感谢支持，已点 Star')).toBeInTheDocument();
  });

  it.each(['identity change', 'closing'])('ignores a delayed successful local write after %s', async kind => {
    let finish!: (result: typeof localSuccess) => void;
    vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [] });
    vi.mocked(api.writeLocalGitHubStar).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const view = mount();
    await announce();
    fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' }));
    if (kind === 'identity change') view.rerender(host({ userId: 'owner-2' }));
    else fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    await act(async () => finish(localSuccess));
    expect(screen.queryByText('感谢支持，已点 Star')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
  });

  it('a successful CLI return resumes the existing invitation, rechecks the account, and waits for a click', async () => {
    route.state = { githubStarResume: { userId: 'owner-1', opportunity, connectionId: 'conn-alice' } };
    mount();
    await act(async () => {});
    expect(api.readGitHubStarAccounts).toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: '支持 LazyMind' })).toBeInTheDocument();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(api.writeGitHubStar).toHaveBeenCalledWith({
      connection_id: 'conn-alice', expected_account_id: '10001', confirmed: true,
    });
  });

  it('a return belonging to another LazyMind identity cannot resume or write', async () => {
    route.state = { githubStarResume: { userId: 'other-owner', opportunity, connectionId: 'conn-alice' } };
    mount();
    await act(async () => {});
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
  });

  it.each([
    [403, 'permission_denied', /权限不足/],
    [503, 'result_unknown', /暂未确认是否成功/],
    [503, 'unavailable', /失败|暂时无法|重试/],
  ])('failure %s/%s never becomes successful support', async (status, reason, message) => {
    vi.mocked(api.writeGitHubStar).mockRejectedValue({ response: { status, data: { reason } } });
    await offer();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
    expect(screen.getByRole('alert')).toHaveTextContent(message);
    expect(screen.queryByText('感谢支持，已点 Star')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
  });

  it('does not replay a pending write after double clicking', async () => {
    let finish!: (result: api.GitHubStarResult) => void;
    vi.mocked(api.writeGitHubStar).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    await offer();
    const button = screen.getByRole('button', { name: '为项目点 Star' });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(api.writeGitHubStar).toHaveBeenCalledTimes(1);
    await act(async () => finish(success));
    expect(screen.getByText('感谢支持，已点 Star')).toBeInTheDocument();
  });

  it('an old identity cannot receive a delayed successful write response', async () => {
    let finish!: (result: api.GitHubStarResult) => void;
    vi.mocked(api.writeGitHubStar).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const view = mount();
    await announce();
    fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' }));
    view.rerender(host({ userId: 'owner-2' }));
    await act(async () => finish(success));
    expect(screen.queryByText('感谢支持，已点 Star')).not.toBeInTheDocument();
    expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
  });

  it('a delayed qualification cannot open over a new result or active execution', async () => {
    let finish!: (result: api.GitHubStarClaim) => void;
    vi.mocked(api.claimGitHubStarPrompt).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const view = mount();
    await announce();
    expect(api.claimGitHubStarPrompt).toHaveBeenCalled();
    view.rerender(host({ blocked: true }));
    await act(async () => finish({ show: true, task_id: opportunity.task_id, reason: 'eligible',
      min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
