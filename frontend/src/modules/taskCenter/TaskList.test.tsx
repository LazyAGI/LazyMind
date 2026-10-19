import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import TaskList from './TaskList';
import type { Task } from './api';
import { TASKS_REMOVED_EVENT } from './events';
import { CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';

const mocks = vi.hoisted(() => ({ listTasks: vi.fn(), getTaskRemovalPreview: vi.fn(), removeTask: vi.fn() }));
vi.mock('./api', () => mocks);
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'failure' }));
vi.mock('./TaskDetail', () => ({ default: () => null, StatusTag: () => null, formatDate: () => '' }));
vi.mock('@/components/StateGraphModal', () => ({ default: () => null }));
vi.mock('@/modules/chat/components/ArchiveConversationModal', () => ({ default: () => null }));
vi.mock('@/modules/settings/recoveryApi', () => ({ unarchiveConversation: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

const task: Task = {
  id: 't', user_id: 'u', conversation_id: 'c', conversation_state: 'active', task_type: 'background_chat',
  title: 'Temporary task', status: 'failed', steps: [], created_at: '', updated_at: '',
};

it('reloads related task records when a batch archive is undone', async () => {
  mocks.listTasks.mockResolvedValue({ items: [], total: 0 });
  render(<TaskList active status='' page={1} onStatusChange={vi.fn()} onPageChange={vi.fn()} />);
  await waitFor(() => expect(mocks.listTasks).toHaveBeenCalledOnce());
  mocks.listTasks.mockResolvedValue({ items: [task], total: 1 });
  act(() => window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT)));
  await screen.findByText(task.title!, { selector: 'strong' });
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
