import { act, cleanup, render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { Task } from '@/modules/taskCenter/api';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import GitHubStarProvider from './GitHubStarProvider';
import * as api from './api';

vi.mock('./GitHubStarPrompt', () => ({ default: () => null }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), findGitHubStarOpportunity: vi.fn(), listGitHubStarWorkTasks: vi.fn() }));
beforeEach(() => { vi.mocked(api.listGitHubStarWorkTasks).mockResolvedValue([
  { id: 'root-3', conversation_id: 'work-1', status: 'running', task_type: 'workflow_run' } as Task,
]); });
afterEach(() => { cleanup(); useConversationRunningStore.setState({ entries: {} }); vi.clearAllMocks(); });
const opportunity: api.GitHubStarOpportunity = { task_id: 'root-3', conversation_id: 'work-1',
  status: 'succeeded', result_ready: true, source: 'final_result' };
function status(value: 'running' | 'completed' | 'failed' | 'canceled') {
  useConversationRunningStore.setState({ entries: { 'work-1': {
    status: value === 'running' ? 'running' : 'idle',
    terminalStatus: value === 'running' ? undefined : value, terminalVersion: value === 'running' ? undefined : 'v1',
    confirmedAt: Date.now(),
  } } });
}

it('successful Work completion creates an opportunity without closing a preview or leaving the result', async () => {
  status('running');
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  const received = vi.fn();
  window.addEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    await act(async () => { render(<MemoryRouter initialEntries={['/chat/work-1']}><GitHubStarProvider userId='owner' conversationId='work-1'>Work result</GitHubStarProvider></MemoryRouter>); });
    expect(api.findGitHubStarOpportunity).not.toHaveBeenCalled();
    await act(async () => status('completed'));
    expect(api.findGitHubStarOpportunity).toHaveBeenCalledWith('work-1', undefined, ['root-3']);
    expect(received).toHaveBeenCalledTimes(1);
    expect((received.mock.calls[0][0] as CustomEvent).detail).toEqual(opportunity);
    await act(async () => status('completed'));
    expect(received).toHaveBeenCalledTimes(1);
  } finally { window.removeEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it('opening an already-completed historical result does not create a new completion opportunity', async () => {
  status('completed');
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  const received = vi.fn();
  window.addEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    await act(async () => { render(<MemoryRouter><GitHubStarProvider userId='owner' conversationId='work-1'>Old result</GitHubStarProvider></MemoryRouter>); });
    expect(received).not.toHaveBeenCalled();
  } finally { window.removeEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it.each(['failed', 'canceled'] as const)('a %s Work result does not create a completion opportunity', async value => {
  status('running');
  const received = vi.fn();
  window.addEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    await act(async () => { render(<MemoryRouter><GitHubStarProvider userId='owner' conversationId='work-1'>Work</GitHubStarProvider></MemoryRouter>); });
    await act(async () => status(value));
    expect(api.findGitHubStarOpportunity).not.toHaveBeenCalled();
    expect(received).not.toHaveBeenCalled();
  } finally { window.removeEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it('ordinary conversation completion without a Work root cannot invite', async () => {
  status('running');
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(null);
  const received = vi.fn();
  window.addEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    await act(async () => { render(<MemoryRouter><GitHubStarProvider userId='owner' conversationId='work-1'>Chat</GitHubStarProvider></MemoryRouter>); });
    await act(async () => status('completed'));
    expect(api.findGitHubStarOpportunity).toHaveBeenCalledTimes(1);
    expect(received).not.toHaveBeenCalled();
  } finally { window.removeEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it('a late root lookup cannot create an opportunity after switching users', async () => {
  status('running');
  let finish!: (value: api.GitHubStarOpportunity) => void;
  vi.mocked(api.findGitHubStarOpportunity).mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const received = vi.fn();
  window.addEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    let view!: ReturnType<typeof render>;
    await act(async () => { view = render(<MemoryRouter><GitHubStarProvider userId='owner' conversationId='work-1'>Work</GitHubStarProvider></MemoryRouter>); });
    await act(async () => status('completed'));
    view.rerender(<MemoryRouter><GitHubStarProvider userId='other' conversationId='work-1'>Work</GitHubStarProvider></MemoryRouter>);
    await act(async () => finish(opportunity));
    expect(received).not.toHaveBeenCalled();
  } finally { window.removeEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});
