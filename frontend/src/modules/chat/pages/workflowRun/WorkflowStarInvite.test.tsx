import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import { axiosInstance } from '@/components/request';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import type { WorkflowControlView } from '@/modules/chat/utils/workflowControl';
import WorkflowStarInvite from './WorkflowStarInvite';

const fixture = vi.hoisted(() => ({ identity: 'owner-session', prompt: vi.fn() }));
vi.mock('@/components/auth', () => ({
  AgentAppsAuth: { getSessionIdentity: () => fixture.identity },
  AUTH_USER_CHANGE_EVENT: 'test-user-change', AUTH_LOGOUT_EVENT: 'test-logout',
}));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { get: vi.fn() } }));
vi.mock('@/modules/githubStar/GitHubStarPrompt', () => ({ default: (props: unknown) => {
  fixture.prompt(props); return <div role='dialog'>plugin invitation</div>;
} }));

const session = { session_id: 'run-1', controller_host: 'external-agent' } as WorkflowSession;
const control = { session_id: 'run-1', continuation: 'completed', binding: { provider: 'codex', bound: true } } as WorkflowControlView;
const task = { id: 'root-1', conversation_id: 'result-conversation', workflow_session_id: 'run-1',
  task_type: 'workflow_run', status: 'succeeded' };
const response = (items: unknown[] = [task]) => ({ data: { items } });
const view = (overrides: Partial<Parameters<typeof WorkflowStarInvite>[0]> = {}) =>
  <WorkflowStarInvite userId='owner' session={session} control={control} resultsOnly={false} {...overrides} />;

beforeEach(() => {
  vi.clearAllMocks(); fixture.identity = 'owner-session';
  vi.mocked(axiosInstance.get).mockResolvedValue(response());
});
afterEach(cleanup);

it.each(['codex', 'workbuddy', 'deepseek-harness'])('uses the same exact completed run and immediate invitation for %s', async provider => {
  const globalOpportunity = vi.fn();
  window.addEventListener('lazymind:github-star-opportunity', globalOpportunity);
  vi.mocked(axiosInstance.get).mockResolvedValue(response([
    { ...task, id: 'other-root', workflow_session_id: 'other-run' }, task,
  ]));
  const props = { control: { ...control, binding: { provider, bound: true } } };
  const mounted = render(view(props));
  await screen.findByRole('dialog');
  expect(axiosInstance.get).toHaveBeenCalledWith('/api/core/task-center/tasks', expect.objectContaining({
    params: { conversation_id: undefined, workflow_session_id: 'run-1', page_size: 100 },
  }));
  expect(fixture.prompt).toHaveBeenLastCalledWith(expect.objectContaining({ userId: 'owner', mode: 'plugin', opportunity: {
    task_id: 'root-1', conversation_id: 'result-conversation', status: 'succeeded', result_ready: true, source: 'final_result',
  } }));
  mounted.rerender(view({ control: { ...props.control, state_version: 9 } }));
  expect(axiosInstance.get).toHaveBeenCalledTimes(1);
  expect(globalOpportunity).not.toHaveBeenCalled();
  window.removeEventListener('lazymind:github-star-opportunity', globalOpportunity);
});

it('waits for whole-run completion and leaves LazyMind result views and other hosts untouched', async () => {
  const mounted = render(view({ control: { ...control, continuation: 'awaiting_user' } }));
  for (const overrides of [
    { resultsOnly: true },
    { control: { ...control, continuation: 'failed' } },
    { session: { ...session, controller_host: 'lazymind' } },
    { control: { ...control, binding: { provider: 'raccoon', bound: true } } },
  ]) mounted.rerender(view(overrides));
  expect(axiosInstance.get).not.toHaveBeenCalled();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  mounted.rerender(view());
  await screen.findByRole('dialog');
});

it('requires a successful root with an actual result conversation', async () => {
  vi.mocked(axiosInstance.get).mockResolvedValue(response([{ ...task, conversation_id: '' }]));
  const mounted = render(view());
  await waitFor(() => expect(axiosInstance.get).toHaveBeenCalledOnce());
  await act(async () => {});
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  vi.mocked(axiosInstance.get).mockResolvedValue(response([{ ...task, status: 'running', workflow_session_id: 'run-2' }]));
  mounted.rerender(view({ session: { ...session, session_id: 'run-2' }, control: { ...control, session_id: 'run-2' } }));
  await waitFor(() => expect(axiosInstance.get).toHaveBeenCalledTimes(2));
  await act(async () => {});
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('discards a late result after switching runs or signing out', async () => {
  let resolve!: (value: ReturnType<typeof response>) => void;
  vi.mocked(axiosInstance.get).mockReturnValueOnce(new Promise(done => { resolve = done; }));
  const mounted = render(view());
  mounted.rerender(view({ session: { ...session, session_id: 'run-2' }, control: { ...control, session_id: 'run-2' } }));
  await act(async () => resolve(response()));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  mounted.rerender(view());
  await screen.findByRole('dialog');
  act(() => {
    fixture.identity = 'different-session';
    window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT));
  });
  expect(AgentAppsAuth.getSessionIdentity()).toBe('different-session');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
