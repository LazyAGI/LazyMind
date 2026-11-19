import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import type { AxiosResponse } from 'axios';
import { ConfigProvider } from 'antd';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import type { Task } from '@/modules/taskCenter/api';
import GitHubStarProvider from './GitHubStarProvider';
import * as api from './api';
import i18n from '@/i18n';

// Keep the real Task Center lookup: only its HTTP boundary is substituted.
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(),
  readGitHubStarAccounts: vi.fn(), claimGitHubStarPrompt: vi.fn(), writeGitHubStar: vi.fn(), markGitHubStarSupported: vi.fn(),
}));
const account = { repository: 'LazyAGI/LazyMind', can_star: true, accounts: [{
  connection_id: 'conn', account_id: '10001', login: 'alice', state: 'not_starred' as const, eligible: true,
}] };
let roots: Task[];
let confirmation = 0;
function root(id: string, status: Task['status']): Task {
  return { id, status, conversation_id: 'work-a', task_type: 'workflow_run', workflow_session_id: id + '-session' } as Task;
}
function state(id: string, status: 'running' | 'completed') {
  useConversationRunningStore.setState(current => ({ entries: { ...current.entries, [id]: {
    status: status === 'running' ? 'running' : 'idle', terminalStatus: status === 'running' ? undefined : 'completed',
    terminalVersion: status === 'running' ? undefined : id + '-completed', confirmedAt: ++confirmation,
  } } }));
}
function host(id = 'work-a') {
  return <ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter>
    <GitHubStarProvider userId='owner' conversationId={id}>Work view</GitHubStarProvider>
  </MemoryRouter></ConfigProvider>;
}
async function mountRunning() {
  state('work-a', 'running');
  let view!: ReturnType<typeof render>;
  await act(async () => { view = render(host()); });
  return view;
}
async function complete() {
  roots = roots.map(row => ({ ...row, status: 'succeeded' }));
  await act(async () => state('work-a', 'completed'));
}
function visible(value: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value });
  document.dispatchEvent(new Event('visibilitychange'));
}
beforeEach(async () => {
  vi.clearAllMocks(); confirmation = 0;
  visible('visible');
  roots = [root('new-root', 'running')];
  await i18n.changeLanguage('zh-CN');
  vi.spyOn(axiosInstance, 'get').mockImplementation(async (url, config) => {
    expect(url).toMatch(/\/api\/core\/task-center\/tasks$/);
    const id = config?.params?.conversation_id;
    return { data: { items: roots.filter(row => row.conversation_id === id) } } as AxiosResponse;
  });
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue(account);
  vi.mocked(api.markGitHubStarSupported).mockResolvedValue(undefined);
  vi.mocked(api.claimGitHubStarPrompt).mockImplementation(async opportunity => ({
    show: true, reason: 'eligible', task_id: opportunity.task_id, min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
  }));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); useConversationRunningStore.setState({ entries: {} }); visible('visible'); });

it('a historical successful Workflow root cannot be reused by a new ordinary reply', async () => {
  roots = [root('old-root', 'succeeded')];
  await mountRunning(); await complete();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
it('the actual Work root observed running can invite when it succeeds', async () => {
  roots = [root('old-root', 'succeeded'), root('new-root', 'running')];
  await mountRunning(); await complete();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledWith(expect.objectContaining({ task_id: 'new-root' }));
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});
it('a Work root first visible on a later activity poll still binds to that run', async () => {
  roots = []; await mountRunning();
  roots = [root('late-root', 'running')];
  await act(async () => state('work-a', 'running'));
  await complete();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledWith(expect.objectContaining({ task_id: 'late-root' }));
});

it.each(['accounts', 'claim'] as const)('idle-to-idle conversation switch invalidates a late %s response', async stage => {
  let finish!: () => void;
  if (stage === 'accounts') vi.mocked(api.readGitHubStarAccounts).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve(account); }));
  else vi.mocked(api.claimGitHubStarPrompt).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve({
    show: true, reason: 'eligible', task_id: 'new-root', min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
  }); }));
  const view = await mountRunning(); await complete();
  await act(async () => state('idle-b', 'completed'));
  view.rerender(host('idle-b'));
  await act(async () => finish());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it.each(['accounts', 'claim'] as const)('a %s response received while hidden waits until visible and claims only once', async stage => {
  let finish!: () => void;
  if (stage === 'accounts') vi.mocked(api.readGitHubStarAccounts).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve(account); }));
  else vi.mocked(api.claimGitHubStarPrompt).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve({
    show: true, reason: 'eligible', task_id: 'new-root', min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
  }); }));
  await mountRunning(); await complete();
  await act(async () => visible('hidden'));
  await act(async () => finish());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  await act(async () => visible('visible'));
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('authentication return binds to its destination scope and still waits for an explicit Star click', async () => {
  roots = [root('new-root', 'succeeded')];
  vi.mocked(api.writeGitHubStar).mockResolvedValue({ repository: 'LazyAGI/LazyMind', connection_id: 'conn',
    account_id: '10001', login: 'alice', state: 'starred', write_performed: true });
  await act(async () => { render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[{
    pathname: '/task-center', state: { githubStarResume: { userId: 'owner', connectionId: 'conn', opportunity: {
      task_id: 'new-root', conversation_id: 'work-a', status: 'succeeded', result_ready: true, source: 'final_result',
    } } },
  }]}><GitHubStarProvider userId='owner' conversationId=''>Work list</GitHubStarProvider></MemoryRouter></ConfigProvider>); });
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
  await act(async () => fireEvent.click(screen.getByRole('button', { name: '为项目点 Star' })));
  expect(api.writeGitHubStar).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).toHaveBeenCalledWith({ connection_id: 'conn', expected_account_id: '10001', confirmed: true });
});

it('switching Work conversations does not reset the application-session invitation limit', async () => {
  const view = await mountRunning(); await complete();
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  await act(async () => fireEvent.click(screen.getByRole('button', { name: /^关闭$/ })));
  roots = [{ ...root('other-root', 'running'), conversation_id: 'work-b' }];
  await act(async () => state('work-b', 'running'));
  await act(async () => view.rerender(host('work-b')));
  roots = roots.map(row => ({ ...row, status: 'succeeded' }));
  await act(async () => state('work-b', 'completed'));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('a newer Work run in the same conversation invalidates an older delayed claim', async () => {
  let finish!: () => void;
  vi.mocked(api.claimGitHubStarPrompt).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve({
    show: true, reason: 'eligible', task_id: 'new-root', min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
  }); }));
  await mountRunning(); await complete();
  await act(async () => visible('hidden'));
  await act(async () => finish());
  // The activity snapshot has not caught up with the new run yet; the real
  // Task Center response must prevent displaying the previous opportunity.
  roots = [root('new-root', 'succeeded'), root('newer-run', 'running')];
  await act(async () => visible('visible'));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('an unclaimed opportunity cannot survive a newer Work run while its account query is hidden', async () => {
  let finish!: () => void;
  vi.mocked(api.readGitHubStarAccounts).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve(account); }));
  await mountRunning(); await complete();
  await act(async () => visible('hidden'));
  await act(async () => finish());
  roots = [root('new-root', 'succeeded'), root('newer-run', 'running')];
  await act(async () => visible('visible'));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('a fresh claim response must still match a successful root without new active Work', async () => {
  let finish!: () => void;
  vi.mocked(api.claimGitHubStarPrompt).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve({
    show: true, reason: 'eligible', task_id: 'new-root', min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3,
  }); }));
  await mountRunning(); await complete();
  roots = [root('new-root', 'succeeded'), root('newer-run', 'running')];
  await act(async () => finish());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
});

it('an authentication return with an empty conversation ID is canceled when its destination page changes', async () => {
  roots = [root('new-root', 'succeeded')];
  let finish!: () => void;
  vi.mocked(api.readGitHubStarAccounts).mockReturnValueOnce(new Promise(resolve => { finish = () => resolve(account); }));
  function Destination() {
    const location = useLocation(), navigate = useNavigate();
    return <GitHubStarProvider userId='owner' conversationId=''>
      <p>{location.pathname}</p><button onClick={() => navigate('/settings')}>打开设置</button>
    </GitHubStarProvider>;
  }
  await act(async () => { render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[{
    pathname: '/task-center', state: { githubStarResume: { userId: 'owner', connectionId: 'conn', opportunity: {
      task_id: 'new-root', conversation_id: 'work-a', status: 'succeeded', result_ready: true, source: 'final_result',
    } } },
  }]}><Destination /></MemoryRouter></ConfigProvider>); });
  expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(1);
  await act(async () => fireEvent.click(screen.getByRole('button', { name: '打开设置' })));
  expect(screen.getByText('/settings')).toBeInTheDocument();
  await act(async () => finish());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});
