import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Modal } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import NotificationSettings from './NotificationSettings';
import ScheduleNotificationPanel from './ScheduleNotificationPanel';
import NotificationHistory from './NotificationHistory';
import RuleEditor from './RuleEditor';
import { emptyRule, type NotificationConfig } from './api';
const mocks = vi.hoisted(() => ({ prefs: vi.fn(), patch: vi.fn(), schedule: vi.fn(), put: vi.fn(), runs: vi.fn(), accounts: vi.fn(), groups: vi.fn(), targets: vi.fn(), execution: vi.fn(), attempts: vi.fn(), retry: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async importOriginal => ({ ...await importOriginal<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts }));
vi.mock('@/modules/taskCenter/api', () => ({ listScheduleTasks: mocks.runs }));
vi.mock('./api', async importOriginal => ({ ...await importOriginal<typeof import('./api')>(), getPreferences: mocks.prefs, patchPreferences: mocks.patch, getScheduleNotifications: mocks.schedule, putScheduleNotifications: mocks.put, getGroups: mocks.groups, getTargets: mocks.targets, getExecutionNotifications: mocks.execution, getAttempts: mocks.attempts, retryNotice: mocks.retry }));
vi.mock('react-i18next', async importOriginal => { const t = (key: string) => key; return { ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t }) }; });
vi.mock('@/runtime/mode', async importOriginal => ({ ...await importOriginal<typeof import('@/runtime/mode')>(), isDesktopRuntime: () => true }));
const mount = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);
const realConfirm = Modal.confirm;
let defaults: NotificationConfig;
beforeEach(() => {
  vi.clearAllMocks(); mocks.groups.mockResolvedValue({ items: [], next_cursor: '' });
  vi.spyOn(Modal, 'confirm').mockImplementation((options: Parameters<typeof Modal.confirm>[0]) => realConfirm({ ...options, transitionName: '', maskTransitionName: '' }));
  defaults = emptyRule(); defaults.channels.desktop = { enabled: true };
  mocks.prefs.mockResolvedValue({ revision: 3, enabled: true, defaults });
  mocks.accounts.mockResolvedValue({ items: [] });
  mocks.schedule.mockResolvedValue({ revision: 0, configured: false, config: null, availability: {} });
  mocks.runs.mockResolvedValue({ items: [], total: 0 });
  mocks.execution.mockResolvedValue({ snapshot: { config: null, revision: 0 }, items: [] });
  mocks.attempts.mockResolvedValue({ items: [], next_cursor: '' });
});
afterEach(async () => { cleanup(); await act(async () => { Modal.destroyAll(); }); await waitFor(() => expect(document.querySelector('.ant-modal-root')).toBeNull()); vi.restoreAllMocks(); });
describe('notification settings and task UI', () => {
  it('requires exact server confirmation to disable notifications', async () => {
    mocks.patch.mockRejectedValueOnce({ response: { data: { data: { detail: { reason: 'NOTIFICATION_CONFIRMATION_REQUIRED', running_task_ids: ['run-1'] } } } } }).mockResolvedValueOnce({ revision: 4, enabled: false, defaults });
    mount(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.global' }));
    expect(await screen.findByText('run-1')).toBeInTheDocument(); expect(mocks.patch).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'notifications.off' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenLastCalledWith({ revision: 3, enabled: false, confirm_running_task_ids: ['run-1'] }));
    expect(await screen.findByText('notifications.paused')).toBeInTheDocument();
  });
  it('retains a conflicting draft without silently overwriting it', async () => {
    mocks.patch.mockRejectedValue({ response: { data: { data: { detail: { reason: 'NOTIFICATION_CONFIG_CONFLICT' } } } } });
    mount(<NotificationSettings />); fireEvent.click(await screen.findByRole('switch', { name: 'notifications.waiting' }));
    expect(await screen.findByText('notifications.conflict')).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'notifications.waiting' })).toBeChecked();
    expect(screen.getByRole('switch', { name: 'notifications.waiting' })).toBeDisabled();
    expect(mocks.patch).toHaveBeenCalledTimes(1);
  });
  it('leaves an old unconfigured task unchanged on viewing and canceling', async () => {
    mount(<ScheduleNotificationPanel scheduleId="old" />);
    expect(await screen.findByText('notifications.unconfigured')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.configure' }));
    await screen.findByRole('switch', { name: 'notifications.succeeded' });
    fireEvent.click(screen.getByRole('button', { name: 'notifications.cancel' })); expect(mocks.put).not.toHaveBeenCalled();
  });
  it('requires explicit recipient selection instead of choosing the first target', async () => {
    mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [{ id: 'a', provider: 'feishu', label: 'Account A', status: 'connected' }] : [] }));
    mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'one', label: 'One', available: true }, { recipient_id: 'two', label: 'Two', available: true }], next_cursor: '' });
    defaults.channels.feishu = { enabled: true };
    const onChange = vi.fn(); mount(<RuleEditor variant="task" value={defaults} onChange={onChange} />);
    fireEvent.mouseDown(await screen.findByRole('combobox', { name: 'notifications.account' }));
    fireEvent.click(await screen.findByText('Account A'));
    await waitFor(() => expect(mocks.targets).toHaveBeenCalledWith('a'));
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'notifications.recipient' }));
    fireEvent.click(await screen.findByText('Two'));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ channels: expect.objectContaining({ feishu: { enabled: true, account_id: 'a', recipient_id: 'two' } }) }));
  });
  it('uses settings channels as switches without choosing a task recipient', async () => {
    mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [{ id: 'a', provider: 'feishu', label: 'Account A', status: 'connected' }] : [] }));
    const onChange = vi.fn(); mount(<RuleEditor value={defaults} onChange={onChange} />);
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.feishu' }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ channels: expect.objectContaining({ feishu: { enabled: true } }) }));
    expect(screen.queryByRole('combobox', { name: 'notifications.recipient' })).not.toBeInTheDocument();
  });
  it('keeps desktop history visible when the external gateway is unavailable', async () => {
    mocks.execution.mockResolvedValue({ snapshot: { config: defaults, revision: 2 }, items: [{ notification_id: 'desktop-1', channel: 'desktop', status: 'delivered', content: 'summary', created_at: '2026-09-17T00:00:00Z' }] });
    mocks.attempts.mockRejectedValue(new Error('offline'));
    mount(<NotificationHistory taskId="run" />);
    expect(await screen.findByText('notifications.delivered')).toBeInTheDocument();
    expect(await screen.findByText('notifications.loadFailed')).toBeInTheDocument();
  });
  it('confirms unknown delivery and reuses the retry key after a lost response', async () => {
    const attempt = { notification_id: 'n', status: 'unknown', retryable: true, retry_of: '', created_at: '2026-09-17T00:00:00Z', payload: { notification_id: 'source', channel: 'wecom', content: 'summary', recipient_id: 'user' } };
    mocks.attempts.mockResolvedValue({ items: [attempt], next_cursor: '' });
    mocks.retry.mockRejectedValueOnce(new Error('lost response')).mockResolvedValueOnce({ ...attempt, notification_id: 'retry', status: 'queued', retryable: false, retry_of: 'n' });
    mount(<NotificationHistory taskId="run" />);
    fireEvent.click(await screen.findByRole('button', { name: 'notifications.retry' }));
    expect(await screen.findByText('notifications.unknownConfirm')).toBeInTheDocument(); expect(mocks.retry).not.toHaveBeenCalled();
    fireEvent.click(screen.getAllByRole('button', { name: 'notifications.retry' }).slice(-1)[0]);
    await waitFor(() => expect(mocks.retry).toHaveBeenCalledTimes(1)); await screen.findByText('notifications.NOTIFICATION_UNAVAILABLE');
    await act(async () => { Modal.destroyAll(); });
    fireEvent.click(await screen.findByRole('button', { name: 'notifications.retry' })); await screen.findByText('notifications.unknownConfirm');
    fireEvent.click(screen.getAllByRole('button', { name: 'notifications.retry' }).slice(-1)[0]);
    await waitFor(() => expect(mocks.retry).toHaveBeenCalledTimes(2));
    expect(mocks.retry.mock.calls[0]).toEqual(mocks.retry.mock.calls[1]); expect(mocks.retry.mock.calls[1]).toEqual(['n', expect.any(String), true]);
  });
});
