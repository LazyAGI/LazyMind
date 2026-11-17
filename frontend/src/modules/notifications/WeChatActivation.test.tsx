import { act, cleanup, fireEvent, render, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import RuleEditor from './RuleEditor';
import { emptyRule, type NotificationConfig, type Preferences } from './api';
import { clearWeChatNotificationState } from './wechatNotificationState';
import WeChatNotificationMonitor from './WeChatNotificationMonitor';

const mocks = vi.hoisted(() => ({ accounts: vi.fn(), targets: vi.fn(), groups: vi.fn(), catalog: vi.fn(), preferences: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async original => ({
  ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts,
}));
vi.mock('./api', async original => ({
  ...await original<typeof import('./api')>(), getTargets: mocks.targets, getGroups: mocks.groups,
  getConfiguredWechatTargets: mocks.catalog, getPreferences: mocks.preferences,
}));
vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('@/runtime/mode', async original => ({
  ...await original<typeof import('@/runtime/mode')>(), isDesktopRuntime: () => true,
}));

const wechatPage = (ready = true, connectionStatus = 'connected') => ({
  items: [{ id: 'wechat-account', provider: 'wechat', label: '微信机器人', status: 'connected',
    capabilities: { notification_ready: ready, connection_check_status: connectionStatus } }],
});
const recipient = (ready = true, connectionStatus = ready ? 'connected' : 'activation_required') => ({
  recipient_id: 'recipient-a', label: '微信会话', available: ready, connection_check_status: connectionStatus,
});
const makeRule = (): NotificationConfig => ({
  ...emptyRule(), channels: { wechat: { enabled: true, account_id: 'wechat-account', recipient_id: 'recipient-a' } },
});
const makePreferences = (defaults: NotificationConfig): Preferences => ({ enabled: true, revision: 3, defaults });
const flushUpdates = async () => { await act(async () => { await vi.advanceTimersByTimeAsync(0); }); };
const advance = async (milliseconds: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(milliseconds); }); };
const focus = async () => { await act(async () => { fireEvent(window, new Event('focus')); }); };
const wechatBlock = (container: HTMLElement) => container.querySelector<HTMLElement>('.notification-channel-block.is-wechat')!;
const status = (container: HTMLElement) => wechatBlock(container).querySelector('.notification-status');
const probes = () => mocks.targets.mock.calls.filter(call => call[3]?.checkConnection === true);
function pairedEditors(config: NotificationConfig, preferences: Preferences, onChange = vi.fn(), monitor = false) {
  return <MemoryRouter>
    {monitor && <WeChatNotificationMonitor userKey="user-a" />}
    <div data-testid="task"><RuleEditor variant="task" value={config} preferences={preferences} onChange={onChange} /></div>
    <div data-testid="settings"><RuleEditor variant="settings" value={preferences.defaults} preferences={preferences} onChange={onChange} /></div>
  </MemoryRouter>;
}
const editorScopes = (container: HTMLElement) => ['task', 'settings'].map(name => container.querySelector<HTMLElement>(`[data-testid="${name}"]`)!);

beforeEach(() => {
  vi.useFakeTimers(); clearWeChatNotificationState();
  mocks.accounts.mockReset().mockImplementation((provider: string) => Promise.resolve(provider === 'wechat' ? wechatPage() : { items: [] }));
  mocks.targets.mockReset().mockResolvedValue({ items: [recipient()], next_cursor: '' });
  mocks.groups.mockReset().mockResolvedValue({ items: [], next_cursor: '' });
  mocks.catalog.mockReset().mockResolvedValue({ items: [] });
  mocks.preferences.mockReset().mockResolvedValue(makePreferences(makeRule()));
});
afterEach(() => { cleanup(); vi.clearAllTimers(); vi.useRealTimers(); });

it('keeps settings connected without recipient checks when its switch opens', async () => {
  const config = makeRule();
  const disabled = { ...config, channels: { wechat: { enabled: false } } };
  const onChange = vi.fn();
  const view = render(<MemoryRouter><RuleEditor value={disabled} onChange={onChange} /></MemoryRouter>);
  await flushUpdates();
  view.rerender(<MemoryRouter><RuleEditor value={config} onChange={onChange} /></MemoryRouter>);
  await flushUpdates(); await advance(40 * 60_000); await focus();
  expect(probes()).toHaveLength(0);
  expect(status(view.container)).toHaveTextContent('notifications.connected');
  expect(within(wechatBlock(view.container)).queryByRole('alert')).not.toBeInTheDocument();
  expect(within(wechatBlock(view.container)).queryByText('notifications.wechatActivationRequired')).not.toBeInTheDocument();
  expect(within(wechatBlock(view.container)).getByRole('switch', { name: 'notifications.wechat' })).toBeChecked();
  expect(onChange).not.toHaveBeenCalled();
});

it.each([{}, { account_id: 'wechat-account' }])('waits for a task recipient confirmation before probing: %j', async partial => {
  const confirmed = makeRule();
  const incomplete = { ...confirmed, channels: { wechat: { enabled: true, ...partial } } };
  const onChange = vi.fn();
  const view = render(<MemoryRouter><RuleEditor variant="task" value={incomplete} preferences={makePreferences(confirmed)} onChange={onChange} /></MemoryRouter>);
  await flushUpdates(); await focus();
  expect(probes()).toHaveLength(0);
  expect(status(view.container)).toHaveTextContent('notifications.wechatRecipientUnconfirmed');
  view.rerender(<MemoryRouter><RuleEditor variant="task" value={confirmed} preferences={makePreferences(confirmed)} onChange={onChange} /></MemoryRouter>);
  await flushUpdates();
  expect(probes()).toEqual([['wechat-account', '', 'recipient-a', { checkConnection: true }]]);
  expect(status(view.container)).toHaveTextContent('notifications.connected');
  expect(onChange).not.toHaveBeenCalled();
});

it('keeps recipient monitoring in tasks and refreshes settings account state on focus', async () => {
  let ready = true;
  mocks.accounts.mockImplementation((provider: string) => Promise.resolve(provider === 'wechat' ? wechatPage(ready) : { items: [] }));
  mocks.targets.mockImplementation(async () => ({ items: [recipient(ready)], next_cursor: '' }));
  const config = makeRule(), preferences = makePreferences(config);
  const original = JSON.stringify(config), onChange = vi.fn();
  mocks.catalog.mockResolvedValue({ items: [{ account_id: 'wechat-account', recipient_id: 'recipient-a' }] });
  const { container } = render(pairedEditors(config, preferences, onChange, true)); await flushUpdates();
  expect(probes()).toHaveLength(1);
  for (const scope of editorScopes(container)) {
    expect(status(scope)).toHaveTextContent('notifications.connected');
    expect(within(wechatBlock(scope)).getByRole('switch', { name: 'notifications.wechat' })).toBeChecked();
  }
  ready = false;
  await advance(20 * 60_000 - 1); expect(probes()).toHaveLength(1);
  await advance(1); expect(probes()).toHaveLength(2);
  const scope = editorScopes(container)[0];
  expect(status(scope)).toHaveTextContent('notifications.pendingActivation');
  expect(within(wechatBlock(scope)).getByText('notifications.wechatActivationRequired')).toBeVisible();
  expect(within(wechatBlock(scope)).getByText('notifications.pendingActivationHint')).toBeVisible();
  expect(within(wechatBlock(scope)).getByRole('switch', { name: 'notifications.wechat' })).toBeChecked();
  expect(within(wechatBlock(editorScopes(container)[0])).queryByText('notifications.noTargets')).not.toBeInTheDocument();
  ready = true; await focus();
  expect(probes()).toHaveLength(3);
  expect(mocks.accounts).toHaveBeenCalledWith('wechat', { silentError: true });
  for (const scope of editorScopes(container)) {
    expect(status(scope)).toHaveTextContent('notifications.connected');
    expect(within(wechatBlock(scope)).queryByText('notifications.wechatActivationRequired')).not.toBeInTheDocument();
  }
  expect(onChange).not.toHaveBeenCalled(); expect(JSON.stringify(config)).toBe(original);
});

it('keeps recipient warnings out of settings while the monitor continues checking saved targets', async () => {
  const config = makeRule(), preferences = makePreferences(config), onChange = vi.fn();
  mocks.catalog.mockResolvedValue({ items: [{ account_id: 'wechat-account', recipient_id: 'recipient-a' }] });
  mocks.targets.mockResolvedValue({ items: [recipient(false)], next_cursor: '' });
  const { container } = render(<MemoryRouter><WeChatNotificationMonitor userKey="user-a" />
    <RuleEditor variant="settings" value={config} preferences={preferences} onChange={onChange} />
  </MemoryRouter>); await flushUpdates();
  expect(status(container)).toHaveTextContent('notifications.connected');
  expect(within(wechatBlock(container)).queryByRole('alert')).not.toBeInTheDocument();
  expect(probes()).toHaveLength(1);
  mocks.targets.mockResolvedValue({ items: [recipient()], next_cursor: '' });
  await focus(); await flushUpdates();
  expect(probes()).toHaveLength(2);
  expect(status(container)).toHaveTextContent('notifications.connected');
  expect(onChange).not.toHaveBeenCalled();
});

it('checks a previously confirmed task immediately when its channel switch is enabled', async () => {
  const config = makeRule();
  const disabled = { ...config, channels: { wechat: { ...config.channels.wechat!, enabled: false } } };
  const onChange = vi.fn();
  const view = render(<MemoryRouter><RuleEditor variant="task" value={disabled} preferences={makePreferences(config)} onChange={onChange} /></MemoryRouter>);
  await flushUpdates(); expect(probes()).toHaveLength(0);
  mocks.targets.mockResolvedValue({ items: [recipient(false)], next_cursor: '' });
  view.rerender(<MemoryRouter><RuleEditor variant="task" value={config} preferences={makePreferences(config)} onChange={onChange} /></MemoryRouter>);
  await flushUpdates();
  expect(probes()).toHaveLength(1);
  expect(status(view.container)).toHaveTextContent('notifications.pendingActivation');
  expect(within(wechatBlock(view.container)).getByRole('switch', { name: 'notifications.wechat' })).toBeChecked();
  expect(onChange).not.toHaveBeenCalled();
});

it('checks a complete unsaved selection once but excludes it from timers, focus and manual reloads', async () => {
  const config = makeRule(), preferences = makePreferences(config);
  mocks.targets.mockResolvedValue({ items: [recipient(false)], next_cursor: '' });
  const { container } = render(pairedEditors(config, preferences, vi.fn(), true));
  await flushUpdates();
  expect(probes()).toHaveLength(1);
  await advance(40 * 60_000); await focus();
  const scope = editorScopes(container)[0];
  fireEvent.click(within(wechatBlock(scope)).getByRole('button', { name: 'notifications.reload' }));
  await flushUpdates();
  expect(mocks.catalog).toHaveBeenCalled();
  expect(probes()).toHaveLength(1);
  expect(mocks.targets.mock.calls.every(call => !call[3]?.checkConnection || call[2] === 'recipient-a')).toBe(true);
});

it.each([
  { state: 'unavailable', label: 'unavailable', hint: 'wechatConnectionUnavailableHint', code: '' },
  { state: 'activation_required', label: 'notConnected', hint: 'wechatReconnectHint', code: 'WECHAT_REAUTHORIZATION_REQUIRED' },
])('keeps recipient-specific $label guidance in the task editor', async ({ state, label, hint, code }) => {
  mocks.targets.mockResolvedValue({ items: [{ ...recipient(state === 'unavailable', state), connection_error_code: code }], next_cursor: '' });
  const config = makeRule(), onChange = vi.fn();
  const { container } = render(pairedEditors(config, makePreferences(config), onChange)); await flushUpdates();
  expect(probes()).toHaveLength(1);
  expect(status(editorScopes(container)[1])).toHaveTextContent('notifications.connected');
  expect(within(wechatBlock(editorScopes(container)[1])).queryByRole('alert')).not.toBeInTheDocument();
  const scope = editorScopes(container)[0];
  expect(status(scope)).toHaveTextContent('notifications.' + label);
  expect(within(wechatBlock(scope)).getByText('notifications.' + hint)).toBeVisible();
  expect(within(wechatBlock(scope)).queryByText('notifications.pendingActivationHint')).not.toBeInTheDocument();
  expect(within(wechatBlock(scope)).getByRole('switch', { name: 'notifications.wechat' })).toBeChecked();
  expect(within(wechatBlock(editorScopes(container)[0])).getByText(`微信会话 · notifications.${label}`)).toBeVisible();
  expect(onChange).not.toHaveBeenCalled();
});

it.each(['master', 'wechat'])('stops checking and hides a cached warning while the global %s gate is paused', async gate => {
  mocks.targets.mockResolvedValue({ items: [recipient(false)], next_cursor: '' });
  const config = makeRule(), preferences = makePreferences(config), onChange = vi.fn();
  const view = render(pairedEditors(config, preferences, onChange)); await flushUpdates();
  expect(status(editorScopes(view.container)[0])).toHaveTextContent('notifications.pendingActivation');
  expect(status(editorScopes(view.container)[1])).toHaveTextContent('notifications.connected');
  const paused = { ...preferences, defaults: { ...preferences.defaults, channels: { ...preferences.defaults.channels } } };
  if (gate === 'master') paused.enabled = false; else paused.defaults.channels.wechat = { enabled: false };
  view.rerender(pairedEditors(config, paused, onChange)); await flushUpdates();
  const count = probes().length;
  await advance(40 * 60_000); await focus();
  expect(probes()).toHaveLength(count);
  for (const scope of editorScopes(view.container)) {
    expect(within(wechatBlock(scope)).queryByText('notifications.wechatActivationRequired')).not.toBeInTheDocument();
    expect(within(wechatBlock(scope)).queryByText('notifications.pendingActivationHint')).not.toBeInTheDocument();
  }
  expect(onChange).not.toHaveBeenCalled();
});

it('does not restart recipient polling when a check resolves after unmount', async () => {
  let finish!: (value: { items: ReturnType<typeof recipient>[] }) => void;
  mocks.targets.mockImplementation((_: string, __: string, ___: string, options?: { checkConnection?: boolean }) => options?.checkConnection
    ? new Promise(resolve => { finish = resolve; }) : Promise.resolve({ items: [recipient()], next_cursor: '' }));
  const config = makeRule(), onChange = vi.fn();
  const view = render(<MemoryRouter><RuleEditor variant="task" value={config} preferences={makePreferences(config)} onChange={onChange} /></MemoryRouter>);
  await flushUpdates(); expect(probes()).toHaveLength(1);
  expect(status(view.container)).toBeNull();
  expect(within(wechatBlock(view.container)).getByRole('status')).toHaveTextContent('notifications.wechatChecking');
  expect(within(wechatBlock(view.container)).queryByText('notifications.wechatActivationRequired')).not.toBeInTheDocument();
  expect(within(wechatBlock(view.container)).queryByText('notifications.wechatConnectionIssue')).not.toBeInTheDocument();
  view.unmount(); await act(async () => { finish({ items: [recipient(false)] }); });
  await advance(40 * 60_000); await focus();
  expect(probes()).toHaveLength(1); expect(onChange).not.toHaveBeenCalled();
});

it.each(['task', 'channel', 'master'])('preserves a confirmed recipient while the %s switch is off and resumes checks on enable', async gate => {
  const config = makeRule(), preferences = makePreferences(config), onChange = vi.fn();
  const disabledConfig = gate === 'task' ? { ...config, channels: { wechat: { ...config.channels.wechat!, enabled: false } } } : config;
  const disabledPreferences = { ...preferences, enabled: gate !== 'master', defaults: gate === 'channel'
    ? { ...preferences.defaults, channels: { wechat: { enabled: false } } } : preferences.defaults };
  const editor = (enabled: boolean) => <MemoryRouter><RuleEditor variant="task"
    value={enabled ? config : disabledConfig} preferences={enabled ? preferences : disabledPreferences} onChange={onChange} />
  </MemoryRouter>;
  const view = render(editor(false)); await flushUpdates();
  expect(status(view.container)).toHaveTextContent('notifications.connected');
  expect(within(wechatBlock(view.container)).queryByText('notifications.wechatRecipientUnconfirmed')).not.toBeInTheDocument();
  expect(probes()).toHaveLength(0);
  view.rerender(editor(true)); await flushUpdates();
  expect(probes()).toHaveLength(1);
  expect(status(view.container)).toHaveTextContent('notifications.connected');
  view.rerender(editor(false)); await flushUpdates();
  await focus(); await advance(40 * 60_000);
  expect(status(view.container)).toHaveTextContent('notifications.connected');
  expect(probes()).toHaveLength(1);
  expect(within(wechatBlock(view.container)).queryByRole('alert')).not.toBeInTheDocument();
  view.rerender(editor(true)); await flushUpdates();
  expect(probes()).toHaveLength(2);
  expect(status(view.container)).toHaveTextContent('notifications.connected');
  expect(onChange).not.toHaveBeenCalled();
  expect(config.channels.wechat).toEqual({ enabled: true, account_id: 'wechat-account', recipient_id: 'recipient-a' });
});
