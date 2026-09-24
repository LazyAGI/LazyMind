import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Modal } from 'antd';
import { MemoryRouter, useLocation } from 'react-router-dom';
import NotificationSettings from './NotificationSettings';
import ScheduleNotificationPanel from './ScheduleNotificationPanel';
import NotificationHistory from './NotificationHistory';
import RuleEditor from './RuleEditor';
import { emptyRule, type NotificationConfig } from './api';
const mocks = vi.hoisted(() => ({ prefs: vi.fn(), patch: vi.fn(), schedule: vi.fn(), put: vi.fn(), tasks: vi.fn(), runs: vi.fn(), accounts: vi.fn(), groups: vi.fn(), targets: vi.fn(), execution: vi.fn(), attempts: vi.fn(), retry: vi.fn(), desktop: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async importOriginal => ({ ...await importOriginal<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts }));
vi.mock('@/modules/taskCenter/api', () => ({ listTasks: mocks.tasks, listScheduleTasks: mocks.runs }));
vi.mock('./api', async importOriginal => ({ ...await importOriginal<typeof import('./api')>(), getPreferences: mocks.prefs, patchPreferences: mocks.patch, getScheduleNotifications: mocks.schedule, putScheduleNotifications: mocks.put, getGroups: mocks.groups, getTargets: mocks.targets, getExecutionNotifications: mocks.execution, getAttempts: mocks.attempts, retryNotice: mocks.retry }));
vi.mock('react-i18next', async importOriginal => { const t = (key: string) => key; return { ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t }) }; });
vi.mock('@/runtime/mode', async importOriginal => ({ ...await importOriginal<typeof import('@/runtime/mode')>(), isDesktopRuntime: mocks.desktop }));
const mount = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);
const LocationProbe = () => { const location = useLocation(); return <output data-testid="location">{location.pathname}{location.search}</output>; };
const realConfirm = Modal.confirm;
let defaults: NotificationConfig;
beforeEach(() => {
  vi.clearAllMocks();
  mocks.groups.mockResolvedValue({ items: [], next_cursor: '' });
  mocks.targets.mockResolvedValue({ items: [], next_cursor: '' });
  mocks.desktop.mockReturnValue(true);
  Object.defineProperty(window, 'Notification', { configurable: true, value: { permission: 'granted', requestPermission: vi.fn() } });
  vi.spyOn(Modal, 'confirm').mockImplementation((options: Parameters<typeof Modal.confirm>[0]) => realConfirm({ ...options, transitionName: '', maskTransitionName: '' }));
  defaults = emptyRule(); defaults.channels.desktop = { enabled: true };
  mocks.prefs.mockResolvedValue({ revision: 3, enabled: true, defaults });
  mocks.accounts.mockResolvedValue({ items: [] });
  mocks.schedule.mockResolvedValue({ revision: 0, configured: false, config: null, availability: {} });
  mocks.tasks.mockResolvedValue({ items: [], total: 0 });
  mocks.runs.mockResolvedValue({ items: [], total: 0 });
  mocks.execution.mockResolvedValue({ snapshot: { config: null, revision: 0 }, items: [] });
  mocks.attempts.mockResolvedValue({ items: [], next_cursor: '' });
});
afterEach(async () => { cleanup(); await act(async () => { Modal.destroyAll(); }); await waitFor(() => expect(document.querySelector('.ant-modal-root')).toBeNull()); vi.restoreAllMocks(); });
describe('notification settings and task UI', () => {
  it('confirms before disabling notifications globally', async () => {
    mocks.patch.mockResolvedValue({ revision: 4, enabled: false, defaults });
    mount(<NotificationSettings />);
    expect(await screen.findByText('notifications.global')).toBeInTheDocument();
    expect(document.querySelector('.notification-global-control')).toBeInTheDocument();
    expect(document.querySelector('.notification-global-icon .anticon-bell')).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.global' }));
    expect((await screen.findAllByText('notifications.confirmGlobal')).length).toBeGreaterThan(0);
    expect(mocks.patch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.confirmClose' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenLastCalledWith({ revision: 3, enabled: false, confirm_running_task_ids: [] }));
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
  it('confirms affected running tasks before closing a default notification channel', async () => {
    mocks.tasks.mockResolvedValue({ items: [{ id: 'run-1', title: '每日 AI 行业动态', status: 'running', schedule_id: 'daily' }], total: 1 });
    mocks.execution.mockResolvedValue({ snapshot: { revision: 1, config: defaults }, items: [] });
    mocks.patch.mockResolvedValue({ revision: 4, enabled: true, defaults: { ...defaults, channels: { ...defaults.channels, desktop: { enabled: false } } } });
    mount(<NotificationSettings />);
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.desktop' }));
    expect(mocks.tasks).toHaveBeenCalledWith({ task_type: 'scheduled', page: 1, page_size: 100 });
    expect((await screen.findAllByText('notifications.closeNamedTitle')).length).toBeGreaterThan(0);
    expect(screen.getByText('每日 AI 行业动态')).toBeInTheDocument();
    expect(screen.getByText('notifications.runningTaskCount')).toBeInTheDocument();
    expect(mocks.patch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.confirmClose' }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
  });
  it('leaves an old unconfigured task unchanged on viewing and canceling', async () => {
    mount(<ScheduleNotificationPanel scheduleId="old" />);
    expect(await screen.findByText('notifications.unconfigured')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.configure' }));
    await screen.findByRole('switch', { name: 'notifications.succeeded' });
    fireEvent.click(screen.getByRole('button', { name: 'notifications.cancel' })); expect(mocks.put).not.toHaveBeenCalled();
  });
  it('confirms before closing a switch in task notification configuration', async () => {
    const configured = emptyRule(); configured.channels.desktop = { enabled: true };
    mocks.schedule.mockResolvedValue({ revision: 2, configured: true, config: configured, availability: {} });
    mocks.runs.mockResolvedValue({ items: [{ id: 'active-run', title: '每日 AI 行业动态', status: 'running' }], total: 1 });
    mocks.execution.mockResolvedValue({ snapshot: { revision: 2, config: configured }, items: [] });
    mount(<ScheduleNotificationPanel scheduleId="daily" title="每日 AI 行业动态" />);
    fireEvent.click(await screen.findByRole('button', { name: 'notifications.configure' }));
    expect(await screen.findByText('notifications.taskOverview')).toBeInTheDocument();
    expect(document.querySelector('.notification-task-overview-icon .anticon-bell')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'notifications.restoreGlobalDefaults' })).toBeInTheDocument();
    expect(document.querySelector('.notification-task-channel-count')).toBeInTheDocument();
    expect(document.querySelector('.notification-rules.is-task .notification-events')).toHaveClass('notification-events');
    expect(document.querySelector('.notification-rules.is-task .notification-channels')).toHaveClass('notification-channels');
    mocks.execution.mockClear();
    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.desktop' }));
    expect((await screen.findAllByText('notifications.closeNamedTitle')).length).toBeGreaterThan(0);
    expect(screen.getByText('每日 AI 行业动态')).toBeInTheDocument();
    expect(screen.getByText('notifications.currentTaskAffected')).toBeInTheDocument();
    expect(screen.getByText('notifications.associated')).toBeInTheDocument();
    expect(mocks.execution).not.toHaveBeenCalled();
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
  it('requires a complete target before enabling a default channel', async () => {
    mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [{ id: 'a', provider: 'feishu', label: 'Account A', status: 'connected' }] : [] }));
    mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'group-a', label: 'Group A', available: true }], next_cursor: '' });
    const onChange = vi.fn(); mount(<RuleEditor value={defaults} onChange={onChange} />);

    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.feishu' }));
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.mouseDown(await screen.findByRole('combobox', { name: 'notifications.account' }));
    fireEvent.click(await screen.findByText('Account A'));
    await waitFor(() => expect(mocks.targets).toHaveBeenCalledWith('a'));
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'notifications.recipient' }));
    fireEvent.click(await screen.findByText('Group A'));

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ channels: expect.objectContaining({ feishu: { enabled: true, account_id: 'a', recipient_id: 'group-a' } }) }));
  });
  it('preserves the saved target when a default channel is toggled', async () => {
    mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [{ id: 'a', provider: 'feishu', label: 'Account A', status: 'connected' }] : [] }));
    defaults.channels.feishu = { enabled: true, account_id: 'a', recipient_id: 'group-a' };
    const onChange = vi.fn(); mount(<RuleEditor value={defaults} onChange={onChange} />);

    fireEvent.click(await screen.findByRole('switch', { name: 'notifications.feishu' }));

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ channels: expect.objectContaining({ feishu: { enabled: false, account_id: 'a', recipient_id: 'group-a' } }) }));
  });
  it('allows replacing an unavailable selected account with another connected account', async () => {
    mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [
      { id: 'a', provider: 'feishu', label: 'Account A', status: 'disconnected' },
      { id: 'b', provider: 'feishu', label: 'Account B', status: 'connected' },
    ] : [] }));
    mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'group-b', label: 'Group B', available: true }], next_cursor: '' });
    defaults.channels.feishu = { enabled: true, account_id: 'a', recipient_id: 'old-group' };
    const onChange = vi.fn(); mount(<RuleEditor variant="task" value={defaults} onChange={onChange} />);

    expect(await screen.findByText('notifications.unavailable')).toBeInTheDocument();
    const accountSelect = screen.getByRole('combobox', { name: 'notifications.account' });
    expect(accountSelect).toBeEnabled();
    fireEvent.mouseDown(accountSelect);
    fireEvent.click(await screen.findByText('Account B'));
    await waitFor(() => expect(mocks.targets).toHaveBeenCalledWith('b'));
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'notifications.recipient' }));
    fireEvent.click(await screen.findByText('Group B'));

    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ channels: expect.objectContaining({ feishu: { enabled: true, account_id: 'b', recipient_id: 'group-b' } }) }));
  });
  it('adds the visual hooks used by the notification settings design', async () => {
    mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'feishu' ? [{ id: 'feishu-1', provider: 'feishu', label: 'Feishu', status: 'connected' }] : [] }));
    const { container } = mount(<RuleEditor value={defaults} onChange={vi.fn()} />);
    await waitFor(() => expect(mocks.accounts).toHaveBeenCalled());
    expect(container.querySelector('.notification-channel-block.is-desktop')).toBeInTheDocument();
    expect(container.querySelector('.notification-channel-block.is-wechat')).toBeInTheDocument();
    expect(container.querySelector('.notification-channel-block.is-feishu')).toBeInTheDocument();
    expect(container.querySelector('.notification-channel-block.is-wecom')).toBeInTheDocument();
    const channelLabels = Array.from(container.querySelectorAll('.notification-channel .notification-grow > strong')).map(node => node.textContent);
    expect(channelLabels).toEqual([
      'notifications.desktop',
      'notifications.feishu',
      'notifications.wecom',
      'notifications.wechat',
    ]);
    expect(container.querySelector('.notification-brand.is-desktop .anticon-desktop')).toBeInTheDocument();
    expect(container.querySelector('.notification-channel-block.is-desktop .notification-status.is-connected')).toHaveTextContent('notifications.authorized');
    expect(container.querySelector('.notification-channel-block.is-feishu .notification-status.is-connected')).toHaveTextContent('notifications.connected');
    expect(container.querySelector('.notification-channel-block.is-wecom .notification-status.is-disconnected')).toHaveTextContent('notifications.notConnected');
    const connectionAction = container.querySelector<HTMLButtonElement>('.notification-link-action');
    expect(connectionAction).toBeInTheDocument();
    expect(connectionAction?.querySelector('.anticon-arrow-right')).toBeInTheDocument();
    const configureActions = Array.from(container.querySelectorAll<HTMLButtonElement>('.notification-configure-action'));
    expect(configureActions).toHaveLength(2);
    configureActions.forEach(action => {
      expect(action).toHaveClass('notification-configure-action');
      expect(action.querySelector('.anticon-arrow-right')).toBeInTheDocument();
    });
    expect(screen.getByText('notifications.newTasksOnly')).toHaveClass('notification-new-task-tag');
  });
  it('shows desktop as authorized only after browser notification permission is granted', async () => {
    mocks.desktop.mockReturnValue(false);
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true });
    Object.defineProperty(navigator, 'locks', { configurable: true, value: {} });
    Object.defineProperty(window, 'Notification', { configurable: true, value: { permission: 'default', requestPermission: vi.fn() } });
    const view = mount(<RuleEditor value={defaults} onChange={vi.fn()} />);
    await waitFor(() => expect(mocks.accounts).toHaveBeenCalled());
    expect(view.container.querySelector('.notification-channel-block.is-desktop .notification-status')).toHaveTextContent('notifications.notAuthorized');
    Object.defineProperty(window.Notification, 'permission', { configurable: true, value: 'granted' });
    window.dispatchEvent(new Event('focus'));
    await waitFor(() => expect(view.container.querySelector('.notification-channel-block.is-desktop .notification-status')).toHaveTextContent('notifications.authorized'));
  });
  it.each(['settings', 'task'] as const)('shows desktop as unauthorized in the %s notification view when enabled without permission', async variant => {
    mocks.desktop.mockReturnValue(true);
    Object.defineProperty(window, 'Notification', { configurable: true, value: { permission: 'default', requestPermission: vi.fn() } });

    const view = mount(<RuleEditor variant={variant} value={defaults} onChange={vi.fn()} />);

    await waitFor(() => expect(mocks.accounts).toHaveBeenCalled());
    expect(screen.getByRole('switch', { name: 'notifications.desktop' })).toBeChecked();
    expect(view.container.querySelector('.notification-channel-block.is-desktop .notification-status')).toHaveTextContent('notifications.notAuthorized');
  });
  it.each(['settings', 'task'] as const)('shows a connected WeChat account as pending activation in the %s notification view', async variant => {
    mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'wechat' ? [{
      id: 'wechat-pending', provider: 'wechat', label: '微信 ClawBot', status: 'connected', runtime_status: 'running',
      capabilities: { notification_ready: false },
    }] : [] }));
    const configured = { ...defaults, channels: { ...defaults.channels, wechat: { enabled: true, account_id: 'wechat-pending' } } };

    const view = mount(<RuleEditor variant={variant} value={configured} onChange={vi.fn()} />);

    await waitFor(() => expect(mocks.accounts).toHaveBeenCalled());
    expect(view.container.querySelector('.notification-channel-block.is-wechat .notification-status')).toHaveTextContent('notifications.pendingActivation');
  });
  it('navigates directly to the matching terminal connection provider', async () => {
    mocks.accounts.mockResolvedValue({ items: [] });
    const view = render(<MemoryRouter initialEntries={['/settings?section=notifications']}><RuleEditor value={defaults} onChange={vi.fn()} /><LocationProbe /></MemoryRouter>);
    const feishuAction = await waitFor(() => view.container.querySelector<HTMLButtonElement>('.notification-channel-block.is-feishu .notification-configure-action'));
    expect(feishuAction).toBeTruthy();
    fireEvent.click(feishuAction!);
    expect(await screen.findByTestId('location')).toHaveTextContent('/settings?section=channels&provider=feishu');
    expect(document.querySelector('.ant-modal-root')).toBeNull();
  });
  it('keeps desktop history visible when the external gateway is unavailable', async () => {
    mocks.execution.mockResolvedValue({ snapshot: { config: defaults, revision: 2 }, items: [{ notification_id: 'desktop-1', channel: 'desktop', status: 'delivered', content: 'summary', created_at: '2026-09-17T00:00:00Z' }] });
    mocks.attempts.mockRejectedValue(new Error('offline'));
    mount(<NotificationHistory taskId="run" />);
    expect(await screen.findByText('notifications.delivered')).toBeInTheDocument();
    expect(await screen.findByText('notifications.loadFailed')).toBeInTheDocument();
  });
  it('confirms unknown delivery and reuses the retry key after a lost response', async () => {
    const attempt = { notification_id: 'n', status: 'unknown', retryable: true, retry_of: '', created_at: '2026-09-17T00:00:00Z', source_notification_id: 'source', payload: { channel: 'wecom', content: 'summary', recipient_id: 'user' } };
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
  it('groups attempts by verified Core source ID without mixing channels', async () => {
    const payload = { channel: 'wecom', content: 'summary', recipient_id: 'user', event: 'succeeded' };
    const failed = { notification_id: 'attempt-a', source_notification_id: 'source-a', status: 'failed', retryable: true, retry_of: '', created_at: '2026-09-21T00:00:00Z', payload };
    const sent = { ...failed, notification_id: 'attempt-b', source_notification_id: 'source-b', status: 'sent', retryable: false, payload: { ...payload, channel: 'feishu' } };
    mocks.execution.mockResolvedValue({ snapshot: { config: defaults, revision: 2 }, items: [
      { ...payload, notification_id: 'source-a', status: 'failed', created_at: failed.created_at },
      { ...sent.payload, notification_id: 'source-b', status: 'sent', created_at: sent.created_at },
    ] });
    mocks.attempts.mockResolvedValue({ items: [failed, sent], next_cursor: '' });
    mount(<NotificationHistory taskId="run" />);
    expect(await screen.findByRole('button', { name: 'notifications.retry' })).toBeEnabled();
    expect(document.querySelectorAll('.notification-history-row')).toHaveLength(2);
  });
  it('disables an ancestor retry when another attempt of the same source is queued', async () => {
    const failed = { notification_id: 'attempt-a', source_notification_id: 'source-a', status: 'failed', retryable: true, retry_of: '', created_at: '2026-09-21T00:00:00Z', payload: { channel: 'wecom', content: 'summary', recipient_id: 'user' } };
    mocks.attempts.mockResolvedValue({ items: [failed, { ...failed, notification_id: 'attempt-retry', status: 'queued', retryable: false, retry_of: 'attempt-a' }], next_cursor: '' });
    mount(<NotificationHistory taskId="run" />);
    expect(await screen.findByRole('button', { name: 'notifications.retry' })).toBeDisabled();
  });

});
