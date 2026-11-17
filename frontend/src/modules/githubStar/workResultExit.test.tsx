import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import { GITHUB_STAR_OPPORTUNITY_EVENT } from './api';
import GitHubStarProvider from './GitHubStarProvider';
import * as api from './api';

vi.mock('./GitHubStarPrompt', () => ({ default: () => null }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), findGitHubStarOpportunity: vi.fn(), listGitHubStarWorkTasks: vi.fn().mockResolvedValue([]) }));
afterEach(() => { cleanup(); useConversationRunningStore.setState({ entries: {} }); vi.clearAllMocks(); });
function ResultView() {
  const navigate = useNavigate(), location = useLocation();
  return <GitHubStarProvider userId='owner' conversationId={location.pathname === '/task-center' ? '' : 'conversation-1'}>
    <p>Work final answer</p><button onClick={() => navigate('/task-center')}>返回 Work 列表</button>
  </GitHubStarProvider>;
}

it.each(['completed', 'failed', 'canceled'] as const)('leaving a historical %s result is not a new Work completion', async terminalStatus => {
  useConversationRunningStore.setState({ entries: { 'conversation-1': {
    status: 'idle', terminalStatus, terminalVersion: 'v1', confirmedAt: Date.now(),
  } } });
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue({ task_id: 'old-root', conversation_id: 'conversation-1',
    status: 'succeeded', result_ready: true, source: 'final_result' });
  const received = vi.fn();
  window.addEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    render(<MemoryRouter initialEntries={['/chat/conversation-1']}><ResultView /></MemoryRouter>);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '返回 Work 列表' })));
    expect(received).not.toHaveBeenCalled();
    expect(api.findGitHubStarOpportunity).not.toHaveBeenCalled();
  } finally { window.removeEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it('leaving running Work does not let a later background completion create an immediate invitation', async () => {
  useConversationRunningStore.setState({ entries: { 'conversation-1': { status: 'running', confirmedAt: Date.now() } } });
  const received = vi.fn();
  window.addEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    render(<MemoryRouter initialEntries={['/chat/conversation-1']}><ResultView /></MemoryRouter>);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '返回 Work 列表' })));
    await act(async () => useConversationRunningStore.setState({ entries: { 'conversation-1': {
      status: 'idle', terminalStatus: 'completed', terminalVersion: 'v2', confirmedAt: Date.now(),
    } } }));
    expect(received).not.toHaveBeenCalled();
    expect(api.findGitHubStarOpportunity).not.toHaveBeenCalled();
  } finally { window.removeEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});
