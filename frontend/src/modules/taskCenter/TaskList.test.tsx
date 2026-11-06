import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import TaskList from './TaskList';
import type { Task } from './api';
import { TASKS_REMOVED_EVENT } from './events';
import { CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';
import { RECOVERY_ARCHIVE_PATH } from '@/modules/settings/recoveryRoute';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';

const mocks = vi.hoisted(() => ({
  listTasks: vi.fn(), getTaskRemovalPreview: vi.fn(), removeTask: vi.fn(),
  navigate: vi.fn(), unarchiveConversation: vi.fn(), messageOpen: vi.fn(), messageSuccess: vi.fn(), messageError: vi.fn(),
  requestGet: vi.fn(),
}));
vi.mock('./api', () => mocks);
vi.mock('react-i18next', () => ({ initReactI18next: { type: '3rdParty', init: vi.fn() }, useTranslation: () => ({ t: (key: string, values?: Record<string, unknown>) => key === 'taskCenter.stepsCompleted' ? `${values?.done}/${values?.total} 个步骤已完成` : key, i18n: { language: 'zh-CN' } }) }));
vi.mock('react-router-dom', () => ({ useNavigate: () => mocks.navigate }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'failure', axiosInstance: { get: mocks.requestGet, defaults: {} }, BASE_URL: '' }));
vi.mock('./TaskDetail', () => ({
  default: ({ task, onArchive }: { task: Task | null; onArchive: (task: Task) => void }) => task && <button onClick={() => onArchive(task)}>Archive task</button>,
  StatusTag: () => null, formatDate: () => '',
}));
vi.mock('@/components/StateGraphModal', () => ({ default: () => null }));
vi.mock('@/modules/chat/components/ArchiveConversationModal', () => ({
  default: ({ open, onArchived }: { open: boolean; onArchived: () => void }) => open && <button onClick={onArchived}>Confirm archive</button>,
}));
vi.mock('@/modules/settings/recoveryApi', () => ({ unarchiveConversation: mocks.unarchiveConversation }));
vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd');
  return { ...actual, message: { ...actual.message, open: mocks.messageOpen, success: mocks.messageSuccess, error: mocks.messageError, destroy: vi.fn() } };
});
afterEach(() => { cleanup(); vi.clearAllMocks(); });
beforeEach(() => {
  mocks.requestGet.mockReset().mockResolvedValue({ data: {} });
  useWorkflowStore.setState({ workflowUIByWorkflow: {} });
});

const task: Task = {
  id: 't', user_id: 'u', conversation_id: 'c', conversation_state: 'active', task_type: 'background_chat',
  title: 'Temporary task', status: 'failed', steps: [], created_at: '', updated_at: '',
};

it.each(['pending', 'running', 'paused', 'failed', 'succeeded', 'canceled'])('uses the complete five-step total in the %s list row', async status => {
  const workflowTask = { ...task, status, task_type: 'workflow_run', workflow_session_id: 'session', steps: [{ step_id: 'one', status: 'succeeded' }] };
  mocks.listTasks.mockResolvedValue({ items: [workflowTask], total: 1 });
  mocks.requestGet.mockImplementation(async (url: string) => ({ data: url.endsWith('/projection') ? {
    graph: { static_order: ['one', 'two', 'three', 'four', 'five'] },
    projection: { nodes: { one: { execution: 'succeeded', branch: 'active' } } },
  } : {} }));
  render(<TaskList active status='' page={1} onStatusChange={vi.fn()} onPageChange={vi.fn()} />);
  expect(await screen.findByText('1/5 个步骤已完成')).toBeInTheDocument();
  expect(screen.queryByText('1/1 个步骤已完成')).not.toBeInTheDocument();
});

it('keeps the original six status categories and uses the grouped attention filter', async () => {
  mocks.listTasks.mockResolvedValue({
    items: [], total: 0,
    status_counts: { all: 10, pending: 1, waiting: 2, waiting_inputs: 3, running: 1, paused: 1, succeeded: 1, completed: 0, failed: 1, interrupted: 0, canceled: 0 },
  });
  const onStatusChange = vi.fn();
  render(<TaskList active status='' page={1} onStatusChange={onStatusChange} onPageChange={vi.fn()} />);

  await waitFor(() => expect(mocks.listTasks).toHaveBeenCalledOnce());
  for (const label of ['taskCenter.statusAttention', 'taskCenter.statusRunning', 'taskCenter.statusPaused', 'taskCenter.statusFailed', 'taskCenter.statusCompleted', 'taskCenter.statusCanceled']) {
    expect(screen.getByText(label)).toBeInTheDocument();
  }
  expect(screen.queryByText('taskCenter.statusInterrupted')).not.toBeInTheDocument();
  expect(screen.queryByText('taskCenter.statusWaiting')).not.toBeInTheDocument();
  expect(screen.queryByText('taskCenter.statusWaitingInputs')).not.toBeInTheDocument();
  fireEvent.click(screen.getByText('taskCenter.statusAttention'));
  expect(onStatusChange).toHaveBeenCalledWith('attention');
});

it.each(['paused', 'failed', 'canceled'])('keeps the existing description without a loading label or partial count for a %s row', async status => {
  const value = { ...task, status, workflow_session_id: 'planning-session', task_type: 'workflow_run', steps: [{ step_id: 'analysis', status }] };
  mocks.listTasks.mockResolvedValue({ items: [value], total: 1 });
  mocks.requestGet.mockImplementation(async (url: string) => {
    if (url.endsWith('/projection')) return { data: { projection: { nodes: {} } } };
    if (url.includes('/workflow-sessions/')) return { data: { session: {
      session_id: 'planning-session', workflow_id: 'ppt', pinned_revision_id: 'planning-revision', steps: [], slots: [],
    } } };
    return { data: { ui: { tab_visibility_ready_material: 'execution_plan', tabs: [{ id: 'analysis', slots: [] }] } } };
  });
  render(<TaskList active status='' page={1} onStatusChange={vi.fn()} onPageChange={vi.fn()} />);
  await waitFor(() => expect(document.querySelector('.task-list-state-copy')).toHaveTextContent(value.title!));
  expect(screen.queryByText('common.loading')).not.toBeInTheDocument();
  expect(screen.queryByText(/个步骤已完成/)).not.toBeInTheDocument();
  expect(screen.queryByText('taskCenter.loadError')).not.toBeInTheDocument();
});

it('retries failed step reads on manual refresh even when the task version is unchanged', async () => {
  const value = { ...task, workflow_session_id: 'retry-session', task_type: 'workflow_run' };
  mocks.listTasks.mockResolvedValue({ items: [value], total: 1 });
  let fail = true;
  mocks.requestGet.mockImplementation(async (url: string) => {
    if (url.endsWith('/projection')) {
      if (fail) throw new Error('temporary failure');
      return { data: { graph: { static_order: ['one'] }, projection: { nodes: { one: { execution: 'succeeded' } } } } };
    }
    return { data: {} };
  });
  render(<TaskList active status='' page={1} onStatusChange={vi.fn()} onPageChange={vi.fn()} />);
  expect(await screen.findByText('taskCenter.loadError')).toBeInTheDocument();
  fail = false;
  fireEvent.click(screen.getByRole('button', { name: 'taskCenter.refresh' }));
  expect(await screen.findByText('1/1 个步骤已完成')).toBeInTheDocument();
  expect(mocks.requestGet).toHaveBeenCalledTimes(4);
});

it('reloads related task records when a batch archive is undone', async () => {
  mocks.listTasks.mockResolvedValue({ items: [], total: 0 });
  render(<TaskList active status='' page={1} onStatusChange={vi.fn()} onPageChange={vi.fn()} />);
  await waitFor(() => expect(mocks.listTasks).toHaveBeenCalledOnce());
  mocks.listTasks.mockResolvedValue({ items: [task], total: 1 });
  act(() => window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT)));
  await screen.findByText(task.title!, { selector: 'strong' });
});

it.each([true, false])('only refreshes archives after a successful task undo following navigation (success=%s)', async success => {
  mocks.listTasks.mockResolvedValue({ items: [task], total: 1 });
  const source = render(<TaskList active status='' page={1} onStatusChange={vi.fn()} onPageChange={vi.fn()} />);
  fireEvent.click(await screen.findByText(task.title!, { selector: 'strong' }));
  fireEvent.click(screen.getByRole('button', { name: 'Archive task' }));
  mocks.listTasks.mockResolvedValue({ items: [], total: 0 });
  fireEvent.click(screen.getByRole('button', { name: 'Confirm archive' }));
  await waitFor(() => expect(mocks.messageOpen).toHaveBeenCalledOnce());
  const notice = render(mocks.messageOpen.mock.calls[0][0].content);
  fireEvent.click(within(notice.container).getByRole('button', { name: 'settingsPage.recovery.viewArchived' }));
  expect(mocks.navigate).toHaveBeenCalledWith(RECOVERY_ARCHIVE_PATH);
  source.unmount();

  const refresh = vi.fn();
  window.addEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, refresh);
  try {
    if (success) mocks.unarchiveConversation.mockResolvedValueOnce(undefined);
    else mocks.unarchiveConversation.mockRejectedValueOnce(new Error('offline'));
    fireEvent.click(within(notice.container).getByRole('button', { name: 'settingsPage.recovery.undo' }));
    expect(mocks.unarchiveConversation).toHaveBeenCalledWith(task.conversation_id);
    if (success) {
      await waitFor(() => expect(mocks.messageSuccess).toHaveBeenCalledWith('settingsPage.recovery.unarchived'));
      expect(refresh).toHaveBeenCalledOnce();
    } else {
      await waitFor(() => expect(mocks.messageError).toHaveBeenCalledWith('failure'));
      expect(refresh).not.toHaveBeenCalled();
    }
  } finally {
    window.removeEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, refresh);
  }
});

it('requires confirmation from the list and preserves rows on cancel or failure, then refreshes after success', async () => {
  mocks.listTasks.mockResolvedValue({ items: [task], total: 1 });
  mocks.getTaskRemovalPreview.mockResolvedValue({ task_id: 't', task_count: 2, conversation_count: 1, active_task_count: 0, impact_version: 'v' });
  const onPageChange = vi.fn();
  render(<TaskList active status='' page={1} onStatusChange={vi.fn()} onPageChange={onPageChange} />);
  await screen.findByText(task.title!, { selector: 'strong' });
  fireEvent.click(screen.getByRole('button', { name: 'taskCenter.removalRowAria' }));
  const dialog = await screen.findByRole('dialog');
  await waitFor(() => expect(within(dialog).getByRole('button', { name: 'settingsPage.recovery.moveToTrash' })).toBeEnabled());
  expect(mocks.removeTask).not.toHaveBeenCalled();
  fireEvent.click(within(dialog).getByRole('button', { name: 'common.cancel' }));
  expect(within(screen.getByRole('table')).getByText(task.title!, { selector: 'strong' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'taskCenter.removalRowAria' }));
  await waitFor(() => expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'settingsPage.recovery.moveToTrash' })).toBeEnabled());
  mocks.removeTask.mockRejectedValueOnce(new Error('failure'));
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'settingsPage.recovery.moveToTrash' }));
  await screen.findByText('taskCenter.taskRemoveFailed');
  expect(within(screen.getByRole('table')).getByText(task.title!, { selector: 'strong' })).toBeInTheDocument();
  expect(mocks.listTasks).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'settingsPage.recovery.moveToTrash' })).toBeEnabled());
  mocks.removeTask.mockImplementationOnce(async () => {
    const detail = { removed_task_ids: ['t', 'related'], trashed_conversation_ids: ['c'], cancellation_pending: false };
    mocks.listTasks.mockResolvedValue({ items: [], total: 0 });
    window.dispatchEvent(new CustomEvent(TASKS_REMOVED_EVENT, { detail }));
    return detail;
  });
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'settingsPage.recovery.moveToTrash' }));
  await waitFor(() => expect(within(screen.getByRole('table')).queryByText(task.title!, { selector: 'strong' })).not.toBeInTheDocument());
  expect(mocks.removeTask).toHaveBeenLastCalledWith('t', 'v');
  expect(mocks.listTasks).toHaveBeenCalledTimes(2);
});

it('returns to the last valid page after affected tasks disappear', async () => {
  mocks.listTasks.mockResolvedValueOnce({ items: [task], total: 21 }).mockResolvedValue({ items: [], total: 20 });
  const onPageChange = vi.fn();
  render(<TaskList active status='' page={2} onStatusChange={vi.fn()} onPageChange={onPageChange} />);
  await screen.findByText(task.title!, { selector: 'strong' });
  act(() => window.dispatchEvent(new CustomEvent(TASKS_REMOVED_EVENT, { detail: { removed_task_ids: ['t'], trashed_conversation_ids: ['c'], cancellation_pending: false } })));
  await waitFor(() => expect(onPageChange).toHaveBeenCalledWith(1));
});
