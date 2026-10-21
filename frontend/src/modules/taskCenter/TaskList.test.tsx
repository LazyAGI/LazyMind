import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import TaskList from './TaskList';
import type { Task } from './api';
import { TASKS_REMOVED_EVENT } from './events';
import { CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';
import { RECOVERY_ARCHIVE_PATH } from '@/modules/settings/recoveryRoute';

const mocks = vi.hoisted(() => ({
  listTasks: vi.fn(), getTaskRemovalPreview: vi.fn(), removeTask: vi.fn(),
  navigate: vi.fn(), unarchiveConversation: vi.fn(), messageOpen: vi.fn(), messageSuccess: vi.fn(), messageError: vi.fn(),
}));
vi.mock('./api', () => mocks);
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('react-router-dom', () => ({ useNavigate: () => mocks.navigate }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'failure' }));
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
