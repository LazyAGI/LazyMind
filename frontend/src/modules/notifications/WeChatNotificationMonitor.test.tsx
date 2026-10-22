import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import WeChatNotificationMonitor from './WeChatNotificationMonitor';
import { emptyRule, NOTIFICATION_PREFERENCES_CHANGED_EVENT, type Preferences } from './api';
import { clearWeChatNotificationState, WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT } from './wechatNotificationState';
import type { ChannelAccount } from '@/modules/channelGateway/api';
import useWeChatNotificationStatus from './useWeChatNotificationStatus';

const mocks = vi.hoisted(() => ({ preferences: vi.fn(), catalog: vi.fn(), targets: vi.fn(), accounts: vi.fn(), identity: 'user-a' }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getPreferences: mocks.preferences,
  getConfiguredWechatTargets: mocks.catalog, getTargets: mocks.targets }));
vi.mock('@/modules/channelGateway/api', async original => ({ ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts }));
vi.mock('@/components/auth', async original => {
  const module = await original<typeof import('@/components/auth')>();
  return { ...module, AgentAppsAuth: { ...module.AgentAppsAuth, getSessionIdentity: () => mocks.identity } };
});
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({
  t: (key: string, options?: { accounts?: string }) => options?.accounts ? `${key}: ${options.accounts}` : key,
}) }));

let preferences: Preferences;
const savedTarget = { account_id: 'wechat-a', recipient_id: 'recipient-a' };
const recipient = (available = true, connection_check_status = available ? 'connected' : 'activation_required') => ({
  recipient_id: 'recipient-a', label: 'Recipient A', available, connection_check_status,
});
const account = (overrides: Partial<ChannelAccount> = {}): ChannelAccount => ({
  id: 'wechat-a', provider: 'wechat', label: 'Wechat A', status: 'connected', runtime_status: 'running',
  connected_at: null, last_poll_at: null, last_message_at: null, last_error: null, updated_at: '',
  capabilities: { notification_ready: true, connection_check_status: 'connected' }, ...overrides,
});
const mount = (userKey = 'user-a') => render(<MemoryRouter><WeChatNotificationMonitor userKey={userKey} /></MemoryRouter>);
const flush = () => act(async () => {});
const changed = async (event = NOTIFICATION_PREFERENCES_CHANGED_EVENT) => { fireEvent(window, new Event(event)); await flush(); };
const configure = () => { mocks.catalog.mockResolvedValue({ items: [savedTarget] }); };
function SettingsRefresh({ refreshKey = 0 }: { refreshKey?: number }) {
  const state = useWeChatNotificationStatus({ enabled: true, aggregate: true, refreshKey });
  return <button onClick={state.refresh}>Refresh settings</button>;
}

beforeEach(() => {
  vi.useFakeTimers(); vi.clearAllMocks(); mocks.identity = 'user-a'; clearWeChatNotificationState();
  preferences = { revision: 1, enabled: true, defaults: emptyRule() };
  preferences.defaults.channels.wechat = { enabled: true };
  mocks.preferences.mockImplementation(async () => preferences);
  mocks.catalog.mockResolvedValue({ items: [] });
  mocks.targets.mockResolvedValue({ items: [recipient()], next_cursor: '' });
  mocks.accounts.mockResolvedValue({ items: [account()] });
});
afterEach(() => { cleanup(); vi.clearAllTimers(); vi.useRealTimers(); });

it('does not probe when a global switch is open but no task has a saved recipient', async () => {
  mount(); await flush();
  await changed();
  fireEvent.focus(window); await flush();
  await act(async () => { await vi.advanceTimersByTimeAsync(20 * 60_000); });
  expect(mocks.catalog).toHaveBeenCalledWith({ silentError: true });
  expect(mocks.targets).not.toHaveBeenCalled();
  expect(mocks.accounts).not.toHaveBeenCalled();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('reloads only saved catalog recipients when settings manually requests a connection refresh', async () => {
  configure(); mocks.targets.mockResolvedValue({ items: [recipient(false)] });
  render(<MemoryRouter><WeChatNotificationMonitor userKey="user-a" /><SettingsRefresh /></MemoryRouter>);
  await flush();
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.wechatActivationRequired');
  expect(mocks.catalog).toHaveBeenCalledTimes(1);
  mocks.targets.mockResolvedValue({ items: [recipient()] });
  fireEvent.click(screen.getByRole('button', { name: 'Refresh settings' })); await flush();
  expect(mocks.catalog).toHaveBeenCalledTimes(2);
  expect(mocks.targets.mock.calls).toEqual([
    ['wechat-a', '', 'recipient-a', { checkConnection: true }],
    ['wechat-a', '', 'recipient-a', { checkConnection: true }],
  ]);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('performs zero provider probes on a settings refresh when the saved catalog is empty', async () => {
  const view = render(<MemoryRouter><WeChatNotificationMonitor userKey="user-a" /><SettingsRefresh /></MemoryRouter>);
  await flush();
  expect(mocks.catalog).toHaveBeenCalledTimes(1);
  view.rerender(<MemoryRouter><WeChatNotificationMonitor userKey="user-a" /><SettingsRefresh refreshKey={1} /></MemoryRouter>);
  await flush();
  expect(mocks.catalog).toHaveBeenCalledTimes(2);
  expect(mocks.targets).not.toHaveBeenCalled();
  expect(mocks.accounts).not.toHaveBeenCalled();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('checks saved recipients on startup and every 20 minutes before any notification is sent', async () => {
  configure(); mount(); await flush();
  expect(mocks.preferences).toHaveBeenCalledWith({ silentError: true });
  expect(mocks.targets).toHaveBeenCalledWith('wechat-a', '', 'recipient-a', { checkConnection: true });
  expect(mocks.accounts).toHaveBeenCalledWith('wechat', { silentError: true });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  mocks.targets.mockResolvedValue({ items: [recipient(false)] });
  await act(async () => { await vi.advanceTimersByTimeAsync(20 * 60_000 - 1); });
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.wechatActivationRequired');
  expect(screen.getByRole('link', { name: 'notifications.wechatOpenSettings' })).toHaveAttribute('href', '/settings?section=notifications');
  expect(mocks.targets).toHaveBeenCalledTimes(2);
});

it.each(['master', 'wechat'])('does not probe or warn when the %s switch is off, and resumes saved recipients after enabling', async gate => {
  configure();
  if (gate === 'master') preferences.enabled = false;
  else preferences.defaults.channels.wechat = { enabled: false };
  mount(); await flush();
  await act(async () => { await vi.advanceTimersByTimeAsync(20 * 60_000); });
  expect(mocks.catalog).not.toHaveBeenCalled();
  expect(mocks.targets).not.toHaveBeenCalled();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  preferences.enabled = true; preferences.defaults.channels.wechat = { enabled: true };
  await changed();
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  expect(mocks.targets).toHaveBeenLastCalledWith('wechat-a', '', 'recipient-a', { checkConnection: true });
});

it('starts checking after a saved recipient is added and stops when the saved rule is cleared', async () => {
  mount(); await flush();
  expect(mocks.targets).not.toHaveBeenCalled();
  configure(); await changed(WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT);
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  mocks.catalog.mockResolvedValue({ items: [] });
  await changed(WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT);
  await act(async () => { await vi.advanceTimersByTimeAsync(40 * 60_000); });
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('filters incomplete catalog entries and probes only the explicit account and recipient pairs', async () => {
  mocks.catalog.mockResolvedValue({ items: [savedTarget, { account_id: 'unused', recipient_id: '' }, { account_id: '', recipient_id: 'unused' }] });
  mocks.accounts.mockResolvedValue({ items: [account(), account({ id: 'unconfigured', status: 'disconnected' })] });
  mount(); await flush();
  expect(mocks.targets.mock.calls).toEqual([['wechat-a', '', 'recipient-a', { checkConnection: true }]]);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('silences warnings on disable and ignores a provider result already in flight', async () => {
  configure();
  let finish!: (value: { items: ReturnType<typeof recipient>[] }) => void;
  mocks.targets.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  mount(); await flush();
  preferences = { ...preferences, enabled: false };
  await changed();
  await act(async () => { finish({ items: [recipient(false)] }); });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  await act(async () => { await vi.advanceTimersByTimeAsync(40 * 60_000); });
  expect(mocks.targets).toHaveBeenCalledTimes(1);
});

it('distinguishes a failed recipient check from activation and clears the warning after focus recovery', async () => {
  configure(); mocks.targets.mockRejectedValueOnce(new Error('network unavailable'));
  mount(); await flush();
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.wechatConnectionCheckFailed');
  expect(screen.getByRole('alert')).not.toHaveTextContent('notifications.pendingActivationHint');
  fireEvent.focus(window); await flush();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(mocks.targets).toHaveBeenCalledTimes(2);
});

it.each([
  { items: [], targets: [recipient()], hint: 'wechatReconnectHint' },
  { items: [account({ status: 'disconnected' })], targets: [recipient()], hint: 'wechatReconnectHint' },
  { items: [account()], targets: [{ ...recipient(false), connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED' }], hint: 'wechatReconnectHint' },
  { items: [account()], targets: [recipient(true, 'unavailable')], hint: 'wechatConnectionCheckFailed' },
  { items: [account()], targets: [], hint: 'wechatTargetUnavailableHint' },
])('provides recipient-specific recovery guidance: $hint', async ({ items, targets, hint }) => {
  configure(); mocks.accounts.mockResolvedValue({ items }); mocks.targets.mockResolvedValue({ items: targets });
  mount(); await flush();
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.' + hint);
});

it('identifies only the configured recipient needing reauthorization when another account is healthy', async () => {
  mocks.catalog.mockResolvedValue({ items: [savedTarget, { account_id: 'expired', recipient_id: 'recipient-b' }] });
  mocks.targets.mockImplementation(async (accountId: string) => ({ items: accountId === 'expired'
    ? [{ ...recipient(false), recipient_id: 'recipient-b', connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED' }]
    : [recipient()] }));
  mocks.accounts.mockResolvedValue({ items: [account(), account({ id: 'expired', label: 'Expired account' })] });
  mount(); await flush();
  expect(screen.getByRole('alert')).toHaveTextContent('notifications.wechatReconnectHint');
  expect(screen.getByRole('alert')).toHaveTextContent('Expired account');
  expect(screen.getByRole('alert')).not.toHaveTextContent('Wechat A');
});

it('does not mistake catalog discovery errors for an expired conversation', async () => {
  mocks.catalog.mockRejectedValue(new Error('core unavailable'));
  mount(); await flush();
  expect(mocks.targets).not.toHaveBeenCalled();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('does not probe from an old preference response after disabling while preferences load', async () => {
  configure();
  let finish!: (value: Preferences) => void;
  const original = preferences;
  mocks.preferences.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  mount(); await flush();
  preferences = { ...preferences, enabled: false };
  await changed();
  await act(async () => { finish(original); });
  expect(mocks.preferences).toHaveBeenCalledTimes(2);
  expect(mocks.targets).not.toHaveBeenCalled();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('isolates old login results and cleans up all checks after unmount', async () => {
  configure();
  let finish!: (value: { items: ReturnType<typeof recipient>[] }) => void;
  mocks.targets.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  const view = mount(); await flush();
  mocks.identity = 'user-b';
  view.rerender(<MemoryRouter><WeChatNotificationMonitor userKey="user-b" /></MemoryRouter>);
  await flush();
  await act(async () => { finish({ items: [recipient(false)] }); });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(mocks.accounts).toHaveBeenCalledTimes(1);
  const calls = mocks.targets.mock.calls.length;
  view.unmount(); fireEvent.focus(window);
  await act(async () => { await vi.advanceTimersByTimeAsync(40 * 60_000); });
  expect(mocks.targets).toHaveBeenCalledTimes(calls);
});
