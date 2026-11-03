import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { message, Modal } from 'antd';
import type { Task } from '@/modules/taskCenter/api';
import { emptyRule, type NotificationConfig, type Preferences } from './api';
import NotificationSettings from './NotificationSettings';

const mocks = vi.hoisted(() => ({ prefs: vi.fn(), patch: vi.fn(), tasks: vi.fn(), task: vi.fn(), execution: vi.fn(), permission: vi.fn() }));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getSessionIdentity: () => 'test-session' } }));
vi.mock('@/modules/taskCenter/api', () => ({ listTasks: mocks.tasks, getTask: mocks.task }));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(),
  getPreferences: mocks.prefs, patchPreferences: mocks.patch, getExecutionNotifications: mocks.execution,
}));
vi.mock('./notificationEvents', async original => ({
  ...await original<typeof import('./notificationEvents')>(), subscribeNotificationChange: () => () => {},
}));
vi.mock('./DesktopNotificationPermission', () => ({ showDesktopNotificationPermission: mocks.permission }));
vi.mock('react-i18next', async original => {
  const t = (key: string) => key;
  return { ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t }) };
});
vi.mock('./RuleEditor', () => ({ default: ({ value, onChange, disabled }: {
  value: NotificationConfig; onChange: (next: NotificationConfig) => void; disabled: boolean;
}) => <div>
  <button role="switch" aria-label="notifications.desktop" aria-checked={Boolean(value.channels.desktop?.enabled)} disabled={disabled}
    onClick={() => onChange({ ...value, channels: { ...value.channels, desktop: { enabled: !value.channels.desktop?.enabled } } })}>Desktop</button>
  <button role="switch" aria-label="notifications.waiting" aria-checked={value.events.waiting.enabled} disabled={disabled}
    onClick={() => onChange({ ...value, events: { ...value.events, waiting: { ...value.events.waiting, enabled: !value.events.waiting.enabled } } })}>Waiting</button>
</div> }));

const realConfirm = Modal.confirm;
let preferences: Preferences;
const confirmationError = (ids: string[]) => ({ response: { data: { data: { detail: { reason: 'NOTIFICATION_CONFIRMATION_REQUIRED', running_task_ids: ids } } } } });
const task = (id: string, title: string, status = 'running'): Task => ({
  id, title, status, task_type: 'background_chat', user_id: 'owner', conversation_id: 'conversation', conversation_state: 'active',
  steps: [], created_at: '2026-10-29', updated_at: '2026-10-29',
});
const dialog = () => screen.getByRole('dialog');
async function openMasterClose() {
  fireEvent.click(await screen.findByRole('switch', { name: 'notifications.global' }));
  await screen.findByRole('button', { name: /notifications\.confirmClose/ });
}
async function confirmClose() {
  fireEvent.click(within(dialog()).getByRole('button', { name: /notifications\.confirmClose/ }));
}

beforeEach(() => {
  vi.resetAllMocks();
  const defaults = emptyRule();
  defaults.channels.desktop = { enabled: true };
  preferences = { revision: 3, enabled: true, defaults };
  mocks.prefs.mockImplementation(async () => preferences);
  mocks.patch.mockImplementation(async patch => {
    preferences = { ...preferences, ...patch, revision: preferences.revision + 1 };
    return preferences;
  });
  mocks.tasks.mockResolvedValue({ items: [], total: 0 });
  mocks.execution.mockResolvedValue({ snapshot: { config: null, revision: 0 }, items: [] });
  vi.spyOn(Modal, 'confirm').mockImplementation((options: Parameters<typeof Modal.confirm>[0]) => realConfirm({ ...options, transitionName: '', maskTransitionName: '' }));
  vi.spyOn(message, 'success').mockImplementation(() => ({ then: vi.fn() }) as never);
  vi.spyOn(message, 'warning').mockImplementation(() => ({ then: vi.fn() }) as never);
});
afterEach(async () => {
  cleanup();
  await act(async () => { Modal.destroyAll(); });
  await waitFor(() => expect(document.querySelector('.ant-modal-root')).toBeNull());
  vi.restoreAllMocks();
});

describe('notification settings regression', () => {
  it('closes the master gate from one review and displays the actual task status', async () => {
    mocks.tasks.mockResolvedValue({ items: [task('existing-id', 'Existing task', 'waiting')], total: 1 });
    render(<NotificationSettings />);
    await openMasterClose();
    expect(within(dialog()).getByText('Existing task')).toBeInTheDocument();
    expect(within(dialog()).getByText('taskCenter.statusWaiting')).toBeInTheDocument();
    expect(within(dialog()).queryByText('existing-id')).not.toBeInTheDocument();
    expect(message.success).not.toHaveBeenCalled();

    await confirmClose();

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(mocks.patch).toHaveBeenCalledTimes(1);
    expect(mocks.patch).toHaveBeenCalledWith({ revision: 3, enabled: false, confirm_running_task_ids: ['existing-id'] });
    expect(Modal.confirm).toHaveBeenCalledTimes(1);
    expect(message.success).not.toHaveBeenCalled();
    expect(message.warning).toHaveBeenCalledTimes(1);
    expect(message.warning).toHaveBeenCalledWith('notifications.paused', 2);
    expect(screen.queryByText('notifications.paused')).not.toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'notifications.global' })).not.toBeChecked();
  });

  it('updates the same review with names when a new task requires confirmation', async () => {
    const existing = task('existing-id', 'Existing task');
    mocks.tasks.mockResolvedValue({ items: [existing], total: 1 });
    mocks.task.mockResolvedValue(task('new-id', 'Newly started task', 'pending'));
    mocks.patch.mockRejectedValueOnce(confirmationError(['existing-id', 'new-id']));
    render(<NotificationSettings />);
    await openMasterClose();
    const originalDialog = dialog();

    await confirmClose();

    await screen.findByText('notifications.confirmationChanged');
    expect(dialog()).toBe(originalDialog);
    expect(Modal.confirm).toHaveBeenCalledTimes(1);
    expect(within(dialog()).getByText('Newly started task')).toBeInTheDocument();
    expect(within(dialog()).getByText('taskCenter.statusPending')).toBeInTheDocument();
    expect(within(dialog()).queryByText('new-id')).not.toBeInTheDocument();
    expect(mocks.task).toHaveBeenCalledWith('new-id');
    expect(message.success).not.toHaveBeenCalled();
    await waitFor(() => expect(within(dialog()).getByRole('button', { name: /notifications\.confirmClose/ })).not.toHaveClass('ant-btn-loading'));

    await confirmClose();

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(mocks.patch).toHaveBeenNthCalledWith(2, { revision: 3, enabled: false, confirm_running_task_ids: ['existing-id', 'new-id'] });
    expect(Modal.confirm).toHaveBeenCalledTimes(1);
    expect(message.success).not.toHaveBeenCalled();
    expect(message.warning).toHaveBeenCalledTimes(1);
    expect(message.warning).toHaveBeenCalledWith('notifications.paused', 2);
  });

  it('keeps confirmation disabled when new task details cannot be loaded', async () => {
    mocks.patch.mockRejectedValueOnce(confirmationError(['new-id']));
    mocks.task.mockRejectedValue(new Error('unavailable'));
    render(<NotificationSettings />);
    await openMasterClose();
    await confirmClose();

    await screen.findByText('notifications.confirmationLoadFailed');
    const confirm = within(dialog()).getByRole('button', { name: /notifications\.confirmClose/ });
    expect(confirm).toBeDisabled();
    expect(Modal.confirm).toHaveBeenCalledTimes(1);
    expect(message.success).not.toHaveBeenCalled();
    fireEvent.click(confirm);
    expect(mocks.patch).toHaveBeenCalledTimes(1);
  });

  it('does not save or announce success when a close review is canceled', async () => {
    render(<NotificationSettings />);
    await openMasterClose();
    fireEvent.click(within(dialog()).getByRole('button', { name: 'notifications.cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(mocks.patch).not.toHaveBeenCalled();
    expect(message.success).not.toHaveBeenCalled();
    expect(message.warning).not.toHaveBeenCalled();
    expect(mocks.permission).not.toHaveBeenCalled();
    expect(screen.getByRole('switch', { name: 'notifications.global' })).toBeChecked();
  });

  it('keeps the saved desktop switch on when a refreshed confirmation is canceled', async () => {
    mocks.patch.mockRejectedValueOnce(confirmationError(['new-id']));
    mocks.task.mockResolvedValue(task('new-id', 'Newly started task', 'pending'));
    render(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.desktop' }));
    await screen.findByRole('button', { name: /notifications\.confirmClose/ });
    await confirmClose();

    await screen.findByText('notifications.confirmationChanged');
    const cancel = within(dialog()).getByRole('button', { name: 'notifications.cancel' });
    await waitFor(() => expect(cancel).toBeEnabled());
    fireEvent.click(cancel);

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    const desktop = screen.getByRole('switch', { name: 'notifications.desktop' });
    await waitFor(() => expect(desktop).toBeEnabled());
    expect(desktop).toBeChecked();
    expect(screen.getByRole('switch', { name: 'notifications.global' })).toBeChecked();
    expect(mocks.patch).toHaveBeenCalledTimes(1);
    expect(message.success).not.toHaveBeenCalled();
    expect(mocks.permission).not.toHaveBeenCalled();
  });

  it('does not announce success or request desktop permission after a failed enable', async () => {
    preferences.defaults.channels.desktop = { enabled: false };
    mocks.patch.mockRejectedValue({ response: { data: { data: { detail: { reason: 'NOTIFICATION_UNAVAILABLE' } } } } });
    render(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.desktop' }));
    await screen.findByText('notifications.NOTIFICATION_UNAVAILABLE');
    expect(message.success).not.toHaveBeenCalled();
    expect(mocks.permission).not.toHaveBeenCalled();
    expect(mocks.patch).toHaveBeenCalledTimes(1);
  });

  it.each(['desktop', 'global'])('prompts for permission only after the %s switch successfully enables desktop notifications', async name => {
    if (name === 'desktop') preferences.defaults.channels.desktop = { enabled: false };
    else preferences.enabled = false;
    let completeSave!: (value: Preferences) => void;
    mocks.patch.mockImplementationOnce(() => new Promise<Preferences>(resolve => { completeSave = resolve; }));
    render(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: `notifications.${name}` }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    expect(message.success).not.toHaveBeenCalled();
    expect(mocks.permission).not.toHaveBeenCalled();

    const saved = { ...preferences, enabled: true, revision: 4, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels, desktop: { enabled: true } } } };
    await act(async () => { completeSave(saved); });

    expect(message.success).toHaveBeenCalledTimes(1);
    expect(message.success).toHaveBeenCalledWith('notifications.saved');
    expect(mocks.permission).toHaveBeenCalledTimes(1);
    mocks.patch.mockResolvedValue({ ...saved, revision: 5, defaults: { ...saved.defaults, events: { ...saved.defaults.events, waiting: { enabled: true, content: 'summary' } } } });
    fireEvent.click(screen.getByRole('switch', { name: 'notifications.waiting' }));
    await waitFor(() => expect(message.success).toHaveBeenCalledTimes(2));
    expect(mocks.permission).toHaveBeenCalledTimes(1);
  });

  it('does not request permission when a channel is enabled while the master gate stays paused', async () => {
    preferences.enabled = false;
    preferences.defaults.channels.desktop = { enabled: false };
    render(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.desktop' }));
    await waitFor(() => expect(message.success).toHaveBeenCalledWith('notifications.saved'));
    expect(screen.getByRole('switch', { name: 'notifications.global' })).not.toBeChecked();
    expect(mocks.permission).not.toHaveBeenCalled();
  });
});
