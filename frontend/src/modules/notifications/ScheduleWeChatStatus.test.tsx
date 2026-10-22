import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { WECHAT_CONNECTION_CHECK_INTERVAL_MS, type ChannelAccount } from '@/modules/channelGateway/api';
import ScheduleNotificationPanel from './ScheduleNotificationPanel';
import { emptyRule, NOTIFICATION_PREFERENCES_CHANGED_EVENT, type NotificationConfig, type Preferences, type ScheduleNotifications } from './api';
import { clearWeChatNotificationState } from './wechatNotificationState';
import WeChatNotificationMonitor from './WeChatNotificationMonitor';
import NotificationSettings from './NotificationSettings';
import { notificationEventScope } from './notificationEvents';
import { AgentAppsAuth } from '@/components/auth';

const mocks = vi.hoisted(() => ({
  prefs: vi.fn(), patch: vi.fn(), schedule: vi.fn(), put: vi.fn(), runs: vi.fn(),
  accounts: vi.fn(), detail: vi.fn(), targets: vi.fn(), groups: vi.fn(), catalog: vi.fn(),
}));
vi.mock('@/modules/channelGateway/api', async original => ({
  ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts,
}));
vi.mock('@/modules/taskCenter/api', () => ({ listScheduleTasks: mocks.runs }));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(), getPreferences: mocks.prefs, patchPreferences: mocks.patch,
  getScheduleNotifications: mocks.schedule, putScheduleNotifications: mocks.put,
  getAccountDetail: mocks.detail, getTargets: mocks.targets, getGroups: mocks.groups,
  getConfiguredWechatTargets: mocks.catalog,
}));
vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('@/runtime/mode', async original => ({
  ...await original<typeof import('@/runtime/mode')>(), isDesktopRuntime: () => true,
}));

const makeRule = (accountId = 'wechat-a', recipientId = 'recipient-a'): NotificationConfig => ({
  ...emptyRule(), channels: { wechat: { enabled: true, account_id: accountId, recipient_id: recipientId } },
});
const makeSchedule = (config = makeRule()): ScheduleNotifications => ({
  revision: 3, configured: true, config, availability: {},
});
const account = (id = 'wechat-a', capabilities: Record<string, unknown> = {}): ChannelAccount => ({
  id, provider: 'wechat', label: id === 'wechat-a' ? 'Selected WeChat A' : 'Selected WeChat B',
  status: 'connected', runtime_status: 'running', updated_at: '',
  connected_at: null, last_poll_at: null, last_message_at: null, last_error: null,
  capabilities: { notification_ready: true, connection_check_status: 'connected', ...capabilities },
});
const flush = async () => { await act(async () => { await vi.advanceTimersByTimeAsync(0); }); };
const tick = async (milliseconds: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(milliseconds); }); };
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
};
const mount = (props: React.ComponentProps<typeof ScheduleNotificationPanel> = {}) => render(
  <MemoryRouter><ScheduleNotificationPanel scheduleId="daily-a" showHistory={false} {...props} /></MemoryRouter>,
);
let preferences: Preferences;
beforeEach(() => {
  vi.useFakeTimers();
  clearWeChatNotificationState();
  vi.clearAllMocks();
  preferences = { revision: 1, enabled: true, defaults: { ...emptyRule(), channels: { wechat: { enabled: true } } } };
  mocks.prefs.mockImplementation(async () => preferences);
  mocks.catalog.mockResolvedValue({ items: [{ account_id: 'wechat-a', recipient_id: 'recipient-a' }] });
  mocks.schedule.mockResolvedValue(makeSchedule());
  mocks.accounts.mockResolvedValue({ items: [account()] });
  mocks.detail.mockImplementation(async (id: string) => account(id));
  mocks.targets.mockImplementation(async (_accountId: string, _cursor = '', recipientId = '') => ({
    items: [{ recipient_id: recipientId || 'recipient-a', label: 'Selected recipient', available: true }], next_cursor: '',
  }));
  mocks.groups.mockResolvedValue({ items: [], next_cursor: '' });
  mocks.runs.mockResolvedValue({ items: [], total: 0 });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllTimers(); vi.useRealTimers(); });

const cases = [
  {
    kind: 'pending', label: 'pendingActivation', hint: 'pendingActivationHint', targetAvailable: false,
    capabilities: { notification_ready: false, connection_check_status: 'activation_required' },
  },
  {
    kind: 'reauthorization', label: 'wechatReconnectRequired', hint: 'wechatReconnectHint', targetAvailable: false,
    capabilities: { notification_ready: false, connection_check_status: 'activation_required', connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED' },
  },
  {
    kind: 'network', label: 'wechatConnectionUnavailable', hint: 'wechatConnectionUnavailableHint', targetAvailable: true,
    capabilities: { notification_ready: true, connection_check_status: 'unavailable' },
  },
];

for (const summaryCard of [true, false]) {
  it.each(cases)(`shows the selected account's $kind state before opening the ${summaryCard ? 'summary' : 'configured channel'} editor`, async ({ label, hint, capabilities, targetAvailable }) => {
    const config = makeRule();
    const originalConfig = JSON.stringify(config);
    // Core can call a transport failure "context required". Repair guidance
    // must come from the exact selected account/recipient check instead.
    mocks.schedule.mockResolvedValue({ ...makeSchedule(config), availability: { wechat: { state: 'unavailable', reason: 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED' } } });
    mocks.accounts.mockResolvedValue({ items: [account('other-account'), account('wechat-a', capabilities)] });
    mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'recipient-a', label: 'Recipient A', available: targetAvailable }], next_cursor: '' });
    const onDraftChange = vi.fn();
    const { container } = mount({ summaryCard, onDraftChange });
    await flush();

    expect(container.querySelector('.notification-wechat-status')).toHaveTextContent('notifications.' + label);
    const warning = screen.getByRole('alert');
    expect(warning).toHaveClass('notification-task-wechat-warning');
    expect(warning).toHaveTextContent('notifications.' + hint);
    expect(warning).toHaveTextContent('Selected WeChat A');
    expect(warning).not.toHaveTextContent('other-account');
    if (label !== 'pendingActivation') expect(warning).not.toHaveTextContent('notifications.pendingActivationHint');
    expect(screen.queryByRole('switch', { name: 'notifications.wechat' })).not.toBeInTheDocument();
    expect(mocks.accounts).toHaveBeenCalledWith('wechat', { silentError: true });
    expect(JSON.stringify(config)).toBe(originalConfig);
    expect(mocks.put).not.toHaveBeenCalled(); expect(mocks.patch).not.toHaveBeenCalled(); expect(onDraftChange).not.toHaveBeenCalled();
  });
}

it('shares saved-recipient monitor results on focus and the regular interval', async () => {
  mocks.accounts.mockResolvedValue({ items: [account('wechat-a', {
    notification_ready: true, connection_check_status: 'activation_required',
  }), account('other-account', { notification_ready: false })] });
  const { container } = render(<MemoryRouter><WeChatNotificationMonitor userKey="user-a" />
    <ScheduleNotificationPanel scheduleId="daily-a" showHistory={false} summaryCard /></MemoryRouter>);
  await flush();
  expect(mocks.targets).toHaveBeenCalledWith('wechat-a', '', 'recipient-a', { checkConnection: true });
  expect(container.querySelector('.notification-wechat-status')).toHaveTextContent('notifications.connected');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();

  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'recipient-a', label: 'Recipient A', available: false }], next_cursor: '' });
  await act(async () => { fireEvent.focus(window); });
  expect(mocks.accounts).toHaveBeenLastCalledWith('wechat', { silentError: true });
  expect(within(container.querySelector('.notification-schedule') as HTMLElement).getByRole('alert')).toHaveTextContent('notifications.pendingActivationHint');

  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'recipient-a', label: 'Recipient A', available: true }], next_cursor: '' });
  await tick(WECHAT_CONNECTION_CHECK_INTERVAL_MS);
  expect(mocks.accounts).toHaveBeenLastCalledWith('wechat', { silentError: true });
  expect(container.querySelector('.notification-wechat-status')).toHaveTextContent('notifications.connected');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(mocks.put).not.toHaveBeenCalled(); expect(mocks.patch).not.toHaveBeenCalled();
});

it.each([
  { trigger: 'focus', gate: 'wechat' }, { trigger: 'storage', gate: 'wechat' },
  { trigger: 'focus', gate: 'master' }, { trigger: 'storage', gate: 'master' },
])('refreshes both pages after another window disables $gate via $trigger without probing', async ({ trigger, gate }) => {
  vi.spyOn(AgentAppsAuth, 'getUserInfo').mockReturnValue({ userId: 'user-a', username: 'A', tenantId: 'tenant-a', token: 'synthetic' });
  const { container } = render(<MemoryRouter><WeChatNotificationMonitor userKey="user-a" />
    <ScheduleNotificationPanel scheduleId="daily-a" showHistory={false} summaryCard />
    <NotificationSettings /></MemoryRouter>);
  await flush();
  const before = mocks.targets.mock.calls.filter(call => call[3]?.checkConnection).length;
  preferences = { ...preferences, revision: 2, enabled: gate !== 'master',
    defaults: { ...preferences.defaults, channels: { wechat: { enabled: gate !== 'wechat' } } } };
  await act(async () => {
    if (trigger === 'focus') fireEvent.focus(window);
    else window.dispatchEvent(new StorageEvent('storage', { key: 'lazymind:notification-change',
      newValue: JSON.stringify({ event: NOTIFICATION_PREFERENCES_CHANGED_EVENT, scope: notificationEventScope() }) }));
  });
  await flush();
  expect(container.querySelector('.notification-wechat-status')).not.toBeInTheDocument();
  expect(screen.getByRole('switch', { name: 'notifications.' + (gate === 'master' ? 'global' : 'wechat') })).not.toBeChecked();
  expect(container.querySelector('.notification-settings .is-wechat .notification-status')).toHaveTextContent('notifications.off');
  await tick(WECHAT_CONNECTION_CHECK_INTERVAL_MS);
  expect(mocks.targets.mock.calls.filter(call => call[3]?.checkConnection)).toHaveLength(before);
  expect(mocks.put).not.toHaveBeenCalled(); expect(mocks.patch).not.toHaveBeenCalled();
});

it('ignores a late focus refresh after a newer cross-window close has loaded', async () => {
  vi.spyOn(AgentAppsAuth, 'getUserInfo').mockReturnValue({ userId: 'user-a', username: 'A', token: 'synthetic' });
  const { container } = mount({ summaryCard: true }); await flush();
  const stale = deferred<Preferences>(), previous = preferences;
  mocks.prefs.mockImplementationOnce(() => stale.promise);
  fireEvent.focus(window); await flush();
  preferences = { ...preferences, revision: 2, enabled: false };
  window.dispatchEvent(new StorageEvent('storage', { key: 'lazymind:notification-change',
    newValue: JSON.stringify({ event: NOTIFICATION_PREFERENCES_CHANGED_EVENT, scope: notificationEventScope() }) }));
  await flush();
  const calls = mocks.targets.mock.calls.length;
  await act(async () => { stale.resolve(previous); });
  expect(container.querySelector('.notification-wechat-status')).not.toBeInTheDocument();
  expect(mocks.targets).toHaveBeenCalledTimes(calls);
});

it('treats a missing saved recipient as unavailable rather than another account activation problem', async () => {
  mocks.targets.mockResolvedValue({ items: [], next_cursor: '' });
  const { container } = mount({ summaryCard: true });
  await flush();
  expect(container.querySelector('.notification-wechat-status')).toHaveTextContent('notifications.wechatTargetUnavailable');
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.wechatTargetUnavailableHint');
  expect(screen.getByRole('alert')).not.toHaveTextContent('notifications.pendingActivationHint');
});

it.each(['master', 'global-channel', 'task-channel'] as const)('does not probe or prompt while the %s gate is disabled', async gate => {
  const config = makeRule();
  if (gate === 'master') preferences.enabled = false;
  if (gate === 'global-channel') preferences.defaults.channels.wechat = { enabled: false };
  if (gate === 'task-channel') config.channels.wechat!.enabled = false;
  mocks.schedule.mockResolvedValue(makeSchedule(config));
  mocks.accounts.mockResolvedValue({ items: [account('wechat-a', { notification_ready: false })] });
  const { container } = mount({ summaryCard: true });
  await flush();
  await tick(WECHAT_CONNECTION_CHECK_INTERVAL_MS);
  await act(async () => { fireEvent.focus(window); });
  expect(mocks.accounts).not.toHaveBeenCalled();
  expect(container.querySelector('.notification-wechat-status')).not.toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

for (const provider of ['feishu', 'wecom', 'wechat'] as const) {
  it.each(['master', 'global-channel', 'task-channel'] as const)(`hides stale unavailable labels for ${provider} while the %s gate is closed`, async gate => {
    const config: NotificationConfig = { ...emptyRule(), channels: {
      [provider]: { enabled: gate !== 'task-channel', account_id: provider + '-a', recipient_id: 'recipient-a' },
    } };
    preferences = { revision: 2, enabled: gate !== 'master', defaults: { ...emptyRule(), channels: {
      [provider]: { enabled: gate !== 'global-channel' },
    } } };
    mocks.schedule.mockResolvedValue({ ...makeSchedule(config), availability: {
      [provider]: { state: 'unavailable', reason: 'NOTIFICATION_TARGET_UNAVAILABLE' },
    } });
    const { container } = mount(); await flush();
    const row = container.querySelector(`.notification-surface .notification-brand.is-${provider}`)?.closest('.notification-row');
    expect(row).toBeDefined();
    const content = within(row as HTMLElement);
    expect(content.getByText('notifications.' + (gate === 'task-channel' ? 'off' : 'on'))).toBeInTheDocument();
    expect(content.queryByText('notifications.unavailable')).not.toBeInTheDocument();
    if (gate !== 'task-channel') expect(content.getByText('notifications.channelPaused')).toBeInTheDocument();
    expect(row?.querySelector('.notification-wechat-status')).not.toBeInTheDocument();
    expect(container.querySelector('.ant-alert-error')).not.toBeInTheDocument();
    expect(mocks.targets).not.toHaveBeenCalled();
    expect(mocks.put).not.toHaveBeenCalled(); expect(mocks.patch).not.toHaveBeenCalled();
  });
}

it.each(['feishu', 'wecom'] as const)('retains the unavailable label for an effectively enabled %s rule', async provider => {
  const config: NotificationConfig = { ...emptyRule(), channels: {
    [provider]: { enabled: true, account_id: provider + '-a', recipient_id: 'recipient-a' },
  } };
  preferences.defaults.channels = { [provider]: { enabled: true } };
  mocks.schedule.mockResolvedValue({ ...makeSchedule(config), availability: {
    [provider]: { state: 'unavailable', reason: 'NOTIFICATION_TARGET_UNAVAILABLE' },
  } });
  const { container } = mount(); await flush();
  const row = container.querySelector(`.notification-surface .notification-brand.is-${provider}`)?.closest('.notification-row');
  expect(within(row as HTMLElement).getByText('notifications.unavailable')).toBeInTheDocument();
});

it('silences a configured task when global preferences change without changing its saved rule', async () => {
  mocks.accounts.mockResolvedValue({ items: [account('wechat-a', { notification_ready: false })] });
  mocks.targets.mockResolvedValue({ items: [{ recipient_id: 'recipient-a', available: false }], next_cursor: '' });
  const { container } = mount({ summaryCard: true });
  await flush();
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.pendingActivationHint');

  preferences = { ...preferences, revision: 2, enabled: false };
  await act(async () => { fireEvent(window, new Event(NOTIFICATION_PREFERENCES_CHANGED_EVENT)); });
  expect(container.querySelector('.notification-wechat-status')).not.toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  const callsAfterDisabling = mocks.accounts.mock.calls.length;
  await tick(WECHAT_CONNECTION_CHECK_INTERVAL_MS);
  expect(mocks.accounts).toHaveBeenCalledTimes(callsAfterDisabling);
  expect(mocks.put).not.toHaveBeenCalled(); expect(mocks.patch).not.toHaveBeenCalled();

  preferences = { ...preferences, revision: 3, enabled: true };
  await act(async () => { fireEvent(window, new Event(NOTIFICATION_PREFERENCES_CHANGED_EVENT)); });
  expect(mocks.accounts).toHaveBeenLastCalledWith('wechat', { silentError: true });
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.pendingActivationHint');
});

it('ignores a previous task account probe after switching to another task and recipient', async () => {
  const oldProbe = deferred<{ items: ChannelAccount[] }>();
  mocks.accounts.mockImplementationOnce(() => oldProbe.promise).mockResolvedValue({ items: [account('wechat-b')] });
  mocks.schedule.mockImplementation(async (id: string) => makeSchedule(id === 'daily-a' ? makeRule() : makeRule('wechat-b', 'recipient-b')));
  const view = mount();
  await flush();
  view.rerender(<MemoryRouter><ScheduleNotificationPanel scheduleId="daily-b" showHistory={false} /></MemoryRouter>);
  await flush();
  await act(async () => { oldProbe.resolve({ items: [account('wechat-a', { notification_ready: false })] }); });

  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  const selected = [...view.container.querySelectorAll('.notification-row')].find(row => row.textContent?.includes('Selected WeChat B'));
  expect(selected).toBeDefined();
  expect(within(selected as HTMLElement).getByText('Selected WeChat B · recipient-b')).toBeInTheDocument();
  expect(view.container.querySelector('.notification-wechat-status')).toHaveTextContent('notifications.connected');
  expect(mocks.put).not.toHaveBeenCalled();
});

it('does not let a late schedule response overwrite a newer task configuration', async () => {
  const oldSchedule = deferred<ScheduleNotifications>();
  mocks.schedule.mockImplementation((id: string) => id === 'daily-a' ? oldSchedule.promise : Promise.resolve(makeSchedule(makeRule('wechat-b', 'recipient-b'))));
  mocks.accounts.mockResolvedValue({ items: [account('wechat-b')] });
  const view = mount();
  await flush();
  view.rerender(<MemoryRouter><ScheduleNotificationPanel scheduleId="daily-b" showHistory={false} /></MemoryRouter>);
  await flush();
  await act(async () => { oldSchedule.resolve(makeSchedule(makeRule())); });

  expect(screen.getByText('Selected WeChat B · recipient-b')).toBeInTheDocument();
  expect(screen.queryByText(/recipient-a/)).not.toBeInTheDocument();
  expect(view.container.querySelector('.notification-wechat-status')).toHaveTextContent('notifications.connected');
  expect(mocks.put).not.toHaveBeenCalled();
});
