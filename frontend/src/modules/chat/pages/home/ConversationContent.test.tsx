import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ConversationContent from './ConversationContent';
import { CHAT_SELECT_CONVERSATION_EVENT } from '@/modules/chat/constants/chat';

const fixture = vi.hoisted(() => ({ detail: vi.fn(), history: vi.fn(), latest: vi.fn(), native: vi.fn(), workflow: vi.fn() }));
vi.mock('@/modules/chat/utils/request', () => ({
  ChatServiceApi: () => ({ conversationServiceGetConversationDetail: fixture.detail, conversationServiceGetConversationHistory: fixture.history }),
  WorkflowSessionApi: () => ({ getLatestSession: fixture.latest }),
}));
vi.mock('../newChat', () => ({ default: () => { fixture.native(); return <><p>Native chat composer</p><input aria-label='Native draft' /></>; } }));
vi.mock('../workflowRun', () => ({ default: (props: { conversationContent?: ReactNode }) => {
  fixture.workflow(props);
  return <><p>External workflow artifacts</p>{props.conversationContent}</>;
} }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

function Navigation() {
  const navigate = useNavigate();
  return <><button onClick={() => navigate('/chat/external')}>Switch conversation</button>
    <button onClick={() => {
      window.dispatchEvent(new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, { detail: { conversationId: 'native-created', source: 'chat' } }));
      navigate('/chat/native-created');
    }}>Native conversation created</button></>;
}
const show = (path = '/chat/external') => render(<MemoryRouter initialEntries={[path]}><Navigation />
  <Routes><Route path='/chat/:conversationId?' element={<ConversationContent />} /></Routes>
</MemoryRouter>);
beforeEach(() => {
  vi.clearAllMocks();
  fixture.latest.mockResolvedValue({ data: { data: { session: null } } });
});
afterEach(cleanup);

it('opens external artifacts directly without mounting local model selection or a composer', async () => {
  fixture.detail.mockResolvedValue({ data: { conversation: {
    display_name: 'Research results', execution_owner: 'external_agent', external_workflow_session_id: 'run-42',
  } } });
  show();
  await screen.findByText('External workflow artifacts');
  expect(fixture.workflow).toHaveBeenLastCalledWith({ sessionId: 'run-42', title: 'Research results', resultsOnly: true });
  expect(fixture.native).not.toHaveBeenCalled();
  expect(fixture.latest).not.toHaveBeenCalled();
});

it('opens a DSH workflow in the workbench with the original read-only questions and replies', async () => {
  fixture.detail.mockResolvedValue({ data: { conversation: { assistant: 'deepseek-harness', display_name: 'Make a PPT' } } });
  fixture.latest.mockResolvedValue({ data: { data: { session: { session_id: 'ppt-42', conversation_id: 'external', controller_host: 'external-agent' } } } });
  fixture.history.mockResolvedValue({ data: { history: [{ id: 'q1', query: 'Make a PPT', result: 'The outline is ready' }] } });
  show();
  await screen.findByText('External workflow artifacts');
  await screen.findByText('The outline is ready');
  expect(fixture.latest).toHaveBeenCalledWith('external', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  expect(fixture.workflow).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: 'ppt-42', resultsOnly: true }));
  expect(screen.getAllByText('Make a PPT')).toHaveLength(1);
  expect(fixture.native).not.toHaveBeenCalled();
});

it('keeps the DSH transcript readable on lookup failure and can retry without mounting a composer', async () => {
  fixture.detail.mockResolvedValue({ data: { conversation: { assistant: 'deepseek-harness' } } });
  fixture.history.mockResolvedValue({ data: { history: [{ id: 'q1', query: 'My original question' }] } });
  fixture.latest.mockRejectedValueOnce(new Error('unavailable'));
  show();
  await screen.findByText('My original question');
  fireEvent.click(screen.getByRole('button', { name: 'chat.workflowRunRefresh' }));
  await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
  expect(screen.getByText('My original question')).toBeInTheDocument();
  expect(fixture.native).not.toHaveBeenCalled();
});

it('ignores a late DSH workflow lookup when a different conversation was opened', async () => {
  let finish!: (value: unknown) => void;
  fixture.detail.mockResolvedValueOnce({ data: { conversation: { assistant: 'deepseek-harness' } } })
    .mockResolvedValueOnce({ data: { conversation: { external_workflow_session_id: 'other-run' } } });
  fixture.latest.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
  show('/chat/previous');
  await waitFor(() => expect(fixture.latest).toHaveBeenCalled());
  fireEvent.click(screen.getByRole('button', { name: 'Switch conversation' }));
  await screen.findByText('External workflow artifacts');
  await act(async () => finish({ data: { data: { session: { session_id: 'old-run', conversation_id: 'previous', controller_host: 'external-agent' } } } }));
  expect(fixture.workflow).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: 'other-run' }));
});

it('reads DSH chat history in chronological order without exposing a native composer', async () => {
  fixture.detail.mockResolvedValue({ data: { conversation: {
    assistant: 'deepseek-harness', chat_executor: 'lazymind', display_name: 'DSH transcript',
  } } });
  fixture.history.mockResolvedValueOnce({ data: { history: [{ id: '2', query: 'Second question', result: 'Second answer' }], next_page_token: 'older' } })
    .mockResolvedValueOnce({ data: { history: [
      { id: '2', query: 'Second question', result: 'Second answer' }, { id: '1', query: 'First question', result: 'First answer' },
    ] } });
  show();
  await screen.findByText('Second answer');
  fireEvent.click(screen.getByRole('button', { name: 'chat.fork.older' }));
  await screen.findByText('First answer');
  expect(screen.getAllByRole('article').map(article => article.textContent)).toEqual(['First questionFirst answer', 'Second questionSecond answer']);
  expect(fixture.native).not.toHaveBeenCalled();
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
});

it('waits for authoritative metadata before mounting a native conversation', async () => {
  let complete!: (value: unknown) => void;
  fixture.detail.mockReturnValue(new Promise(resolve => { complete = resolve; }));
  show('/chat/native');
  expect(fixture.native).not.toHaveBeenCalled();
  act(() => complete({ data: { conversation: { conversation_id: 'native' } } }));
  await screen.findByText('Native chat composer');
  expect(fixture.workflow).not.toHaveBeenCalled();
});

it('keeps a failed metadata read out of local generation and allows a safe retry', async () => {
  fixture.detail.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({
    data: { conversation: { external_workflow_session_id: 'run-recovered' } },
  });
  show();
  fireEvent.click(await screen.findByRole('button', { name: 'chat.fork.retryRead' }));
  await screen.findByText('External workflow artifacts');
  expect(fixture.native).not.toHaveBeenCalled();
});

it('does not turn a malformed external result into a local conversation', async () => {
  fixture.detail.mockResolvedValue({ data: { conversation: { execution_owner: 'external_agent' } } });
  show();
  await screen.findByRole('alert');
  expect(fixture.native).not.toHaveBeenCalled();
  expect(fixture.workflow).not.toHaveBeenCalled();
});

it('unmounts the native composer immediately when selecting a different external conversation', async () => {
  let complete!: (value: unknown) => void;
  fixture.detail.mockResolvedValueOnce({ data: { conversation: { conversation_id: 'native' } } })
    .mockReturnValueOnce(new Promise(resolve => { complete = resolve; }));
  show('/chat/native');
  await screen.findByText('Native chat composer');
  fireEvent.click(screen.getByRole('button', { name: 'Switch conversation' }));
  expect(screen.queryByText('Native chat composer')).not.toBeInTheDocument();
  act(() => complete({ data: { conversation: { external_workflow_session_id: 'run-next' } } }));
  await screen.findByText('External workflow artifacts');
});

it('ignores late metadata from the previous conversation', async () => {
  let complete!: (value: unknown) => void;
  fixture.detail.mockReturnValueOnce(new Promise(resolve => { complete = resolve; }))
    .mockResolvedValueOnce({ data: { conversation: { external_workflow_session_id: 'run-current' } } });
  show('/chat/previous');
  fireEvent.click(screen.getByRole('button', { name: 'Switch conversation' }));
  await screen.findByText('External workflow artifacts');
  act(() => complete({ data: { conversation: { conversation_id: 'previous' } } }));
  await waitFor(() => expect(fixture.workflow).toHaveBeenLastCalledWith({ sessionId: 'run-current', title: undefined, resultsOnly: true }));
  expect(fixture.native).not.toHaveBeenCalled();
});

it('keeps the new-chat entry immediate without reading conversation metadata', () => {
  show('/chat');
  expect(screen.getByText('Native chat composer')).toBeVisible();
  expect(fixture.detail).not.toHaveBeenCalled();
});

it('preserves the native chat instance when its newly created conversation changes the route', async () => {
  let complete!: (value: unknown) => void;
  fixture.detail.mockReturnValue(new Promise(resolve => { complete = resolve; }));
  show('/chat');
  const composer = screen.getByRole('textbox', { name: 'Native draft' });
  fireEvent.change(composer, { target: { value: 'active native draft' } });
  fireEvent.click(screen.getByRole('button', { name: 'Native conversation created' }));
  expect(screen.getByRole('textbox', { name: 'Native draft' })).toBe(composer);
  act(() => complete({ data: { conversation: { conversation_id: 'native-created' } } }));
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Native draft' })).toBe(composer));
  expect(composer).toHaveValue('active native draft');
});
