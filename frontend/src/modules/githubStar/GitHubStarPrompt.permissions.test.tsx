import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import i18n from '@/i18n';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as api from './api';

vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(), readGitHubStarAccounts: vi.fn(),
  claimGitHubStarPrompt: vi.fn(), writeGitHubStar: vi.fn(), markGitHubStarSupported: vi.fn(),
  findGitHubStarOpportunity: vi.fn(),
}));
beforeEach(() => {
  vi.mocked(api.findGitHubStarOpportunity).mockImplementation(async (conversationId, workflowSessionId, taskIds) => {
    if (conversationId !== 'work-1' || workflowSessionId !== undefined) return null;
    const taskId = taskIds?.find(id => ['root-3', 'duplicate-root', 'retry-root', 'limited-root'].includes(id));
    return taskId ? { task_id: taskId, conversation_id: conversationId, status: 'succeeded', result_ready: true, source: 'final_result' } : null;
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it('a caller lacking application write permission does not receive an invitation even with no connection', async () => {
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', accounts: [], can_star: false });
  render(<MemoryRouter><GitHubStarPrompt userId='reader' blocked={false} returnTo='/task-center' /></MemoryRouter>);
  await act(async () => {
    window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: {
      task_id: 'root-3', conversation_id: 'work-1', status: 'succeeded', result_ready: true, source: 'final_result',
    } }));
  });
  expect(api.readGitHubStarAccounts).toHaveBeenCalled();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('two authorized connections for the same GitHub identity still need only one confirmation click', async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  const account = { account_id: '10001', login: 'alice', state: 'not_starred' as const, eligible: true };
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', can_star: true,
    accounts: [{ ...account, connection_id: 'first' }, { ...account, connection_id: 'second' }] });
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({ show: true, reason: 'eligible', task_id: 'duplicate-root',
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 });
  vi.mocked(api.writeGitHubStar).mockResolvedValue({ repository: 'LazyAGI/LazyMind', connection_id: 'first',
    account_id: '10001', login: 'alice', state: 'starred', write_performed: true });
  vi.mocked(api.markGitHubStarSupported).mockResolvedValue(undefined);
  render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter>
    <GitHubStarPrompt userId='owner' blocked={false} returnTo='/task-center' />
  </MemoryRouter></ConfigProvider>);
  await act(async () => {
    window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: {
      task_id: 'duplicate-root', conversation_id: 'work-1', status: 'succeeded', result_ready: true, source: 'final_result',
    } }));
  });
  const button = screen.getByRole('button', { name: '为项目点 Star' });
  expect(button).toBeEnabled();
  await act(async () => fireEvent.click(button));
  expect(api.writeGitHubStar).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).toHaveBeenCalledWith({ connection_id: 'first', expected_account_id: '10001', confirmed: true });
});

it('a failed authentication-return query can be retried from its visible dialog without writing', async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList);
  vi.mocked(api.readGitHubStarAccounts).mockRejectedValueOnce(new Error('offline')).mockResolvedValue({
    repository: 'LazyAGI/LazyMind', can_star: true, accounts: [{ connection_id: 'retry-connection',
      account_id: '10001', login: 'alice', state: 'not_starred', eligible: true }],
  });
  await act(async () => { render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[{
    pathname: '/task-center', state: { githubStarResume: { userId: 'owner', connectionId: 'retry-connection', opportunity: {
      task_id: 'retry-root', conversation_id: 'work-1', status: 'succeeded', result_ready: true, source: 'final_result',
    } } },
  }]}><GitHubStarPrompt userId='owner' blocked={false} returnTo='/task-center' /></MemoryRouter></ConfigProvider>); });
  expect(screen.getByRole('alert')).toBeInTheDocument();
  await act(async () => fireEvent.click(screen.getByRole('button', { name: /重\s*试/ })));
  expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(2);
  expect(screen.getByRole('button', { name: '为项目点 Star' })).toBeEnabled();
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it.each([403, 429])('a GitHub rate limit (%s) asks the user to wait without reauthorization or automatic replay', async status => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', can_star: true,
    accounts: [{ connection_id: 'limited', account_id: '10001', login: 'alice', state: 'not_starred', eligible: true }] });
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({ show: true, reason: 'eligible', task_id: 'limited-root',
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 });
  vi.mocked(api.writeGitHubStar).mockRejectedValue({ response: { status, data: { reason: 'rate_limited' } } });
  render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter>
    <GitHubStarPrompt userId='limited-owner' blocked={false} returnTo='/task-center' />
  </MemoryRouter></ConfigProvider>);
  await act(async () => { window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: {
    task_id: 'limited-root', conversation_id: 'work-1', status: 'succeeded', result_ready: true, source: 'final_result',
  } })); });
  await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
  expect(screen.getByRole('alert')).toHaveTextContent('请等待后再试');
  expect(screen.queryByRole('button', { name: '管理 GitHub 账号' })).not.toBeInTheDocument();
  expect(api.writeGitHubStar).toHaveBeenCalledTimes(1);
  expect(api.markGitHubStarSupported).not.toHaveBeenCalled();
});
