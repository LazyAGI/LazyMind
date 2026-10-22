import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import useWeChatNotificationStatus from './useWeChatNotificationStatus';
import { checkWeChatNotificationTarget, clearWeChatNotificationState } from './wechatNotificationState';
import { WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT } from './notificationEvents';
import type { ChannelAccount } from '@/modules/channelGateway/api';

const mocks = vi.hoisted(() => ({ accounts: vi.fn(), targets: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async original => ({ ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getTargets: mocks.targets }));
const account = (id = 'chosen', capabilities: Record<string, unknown> = {}): ChannelAccount => ({
  id, provider: 'wechat', label: id, status: 'connected', runtime_status: 'running', connected_at: null,
  last_poll_at: null, last_message_at: null, last_error: null, updated_at: '',
  capabilities: { notification_ready: true, connection_check_status: 'connected', ...capabilities },
});
const target = (recipient_id = 'recipient-a', available = true) => ({ recipient_id, label: 'Recipient', available,
  connection_check_status: available ? 'connected' : 'activation_required' });
const props = { enabled: true, accountId: 'chosen', recipientId: 'recipient-a' };
const flush = () => act(async () => {});
beforeEach(() => {
  vi.useFakeTimers(); vi.clearAllMocks(); clearWeChatNotificationState();
  mocks.accounts.mockResolvedValue({ items: [account()] });
  mocks.targets.mockResolvedValue({ items: [target()] });
});
afterEach(() => { cleanup(); vi.clearAllTimers(); vi.useRealTimers(); });

it.each([
  { enabled: true }, { enabled: true, accountId: 'chosen' },
  { enabled: true, recipientId: 'recipient-a' }, { ...props, enabled: false },
])('does not probe before a complete enabled recipient is confirmed: %j', async options => {
  const { result } = renderHook(() => useWeChatNotificationStatus(options)); await flush();
  await act(async () => { result.current.refresh(); window.dispatchEvent(new Event('focus')); await vi.advanceTimersByTimeAsync(40 * 60_000); });
  expect(mocks.targets).not.toHaveBeenCalled();
  expect(mocks.accounts).not.toHaveBeenCalled();
  expect(result.current.status).toBe(options.enabled ? 'unconfigured' : undefined);
});

it('probes only the confirmed pair before reading cached account status', async () => {
  const order: string[] = [];
  mocks.targets.mockImplementation(async () => { order.push('recipient'); return { items: [target('recipient-a', false)] }; });
  mocks.accounts.mockImplementation(async () => { order.push('account'); return { items: [account()] }; });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(mocks.targets).toHaveBeenCalledWith('chosen', '', 'recipient-a', { checkConnection: true });
  expect(mocks.accounts).toHaveBeenCalledWith('wechat', { silentError: true });
  expect(order).toEqual(['recipient', 'account']);
  expect(result.current.status).toBe('pending');
});

it('does not mark a healthy recipient pending because another recipient was rejected', async () => {
  mocks.accounts.mockResolvedValue({ items: [account('chosen', { connection_check_status: 'activation_required' })] });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.status).toBe('connected');
});

it('does not use another account to determine the selected account status', async () => {
  mocks.accounts.mockResolvedValue({ items: [account('foreign', { notification_ready: false }), account()] });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.account?.id).toBe('chosen');
  expect(result.current.status).toBe('connected');
});

it('does not treat a different recipient returned by the server as the selected recipient', async () => {
  mocks.targets.mockResolvedValue({ items: [target('recipient-b')] });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.status).toBe('targetUnavailable');
});

it('does not use a foreign account when the selected account no longer exists', async () => {
  mocks.accounts.mockResolvedValue({ items: [account('foreign')] });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.status).toBe('disconnected');
});

it('distinguishes a missing recipient from an expired context', async () => {
  mocks.targets.mockResolvedValue({ items: [] });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.status).toBe('targetUnavailable');
});

it('treats failed recipient checks as temporary unavailability', async () => {
  mocks.targets.mockRejectedValue(new Error('network'));
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.status).toBe('unavailable');
  expect(mocks.accounts).not.toHaveBeenCalled();
});

it('guides a rejected login to reauthorize without confusing it with activation', async () => {
  mocks.targets.mockResolvedValue({ items: [{ ...target('recipient-a', false), connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED' }] });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.status).toBe('reauthorization');
});

it.each([
  { account: { ...account(), status: 'disconnected' }, expected: 'disconnected' },
  { account: account('chosen', { connection_error_code: 'WECHAT_REAUTHORIZATION_REQUIRED' }), expected: 'reauthorization' },
  { account: account(), expected: 'targetUnavailable' },
])('classifies a target rejection using cached account status: $expected', async ({ account: cached, expected }) => {
  mocks.targets.mockRejectedValue({ response: { data: { error: { code: 'NOTIFICATION_TARGET_UNAVAILABLE' } } } });
  mocks.accounts.mockResolvedValue({ items: [cached] });
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  expect(result.current.status).toBe(expected);
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  expect(mocks.accounts).toHaveBeenCalledWith('wechat', { silentError: true });
});

it('requests catalog refresh on manual settings reload without probing on mount or switch changes', async () => {
  const requested = vi.fn();
  window.addEventListener(WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT, requested);
  try {
    const view = renderHook(value => useWeChatNotificationStatus(value), {
      initialProps: { enabled: true, aggregate: true, refreshKey: 0 },
    }); await flush();
    expect(requested).not.toHaveBeenCalled();
    view.rerender({ enabled: false, aggregate: true, refreshKey: 0 }); await flush();
    view.rerender({ enabled: true, aggregate: true, refreshKey: 0 }); await flush();
    expect(requested).not.toHaveBeenCalled();
    view.rerender({ enabled: true, aggregate: true, refreshKey: 1 }); await flush();
    expect(requested).toHaveBeenCalledTimes(1);
    await act(async () => { view.result.current.refresh(); });
    expect(requested).toHaveBeenCalledTimes(2);
    expect(mocks.targets).not.toHaveBeenCalled();
    expect(mocks.accounts).not.toHaveBeenCalled();
  } finally { window.removeEventListener(WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT, requested); }
});

it('does not enroll a confirmed draft in periodic or focus checks', async () => {
  const { result } = renderHook(() => useWeChatNotificationStatus(props)); await flush();
  await act(async () => { await vi.advanceTimersByTimeAsync(20 * 60_000 - 1); });
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  await act(async () => { window.dispatchEvent(new Event('focus')); });
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  expect(mocks.targets).toHaveBeenLastCalledWith('chosen', '', 'recipient-a', { checkConnection: true });
  expect(mocks.accounts).toHaveBeenLastCalledWith('wechat', { silentError: true });
  expect(result.current.status).toBe('connected');
});

it.each(['recipient', 'account'])('isolates late results after switching the selected %s', async scope => {
  let finish!: (value: { items: ReturnType<typeof target>[] }) => void;
  mocks.targets.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  const view = renderHook(value => useWeChatNotificationStatus(value), { initialProps: props }); await flush();
  const next = { ...props, ...(scope === 'recipient' ? { recipientId: 'recipient-b' } : { accountId: 'new-account' }) };
  mocks.targets.mockResolvedValue({ items: [target(next.recipientId)] });
  mocks.accounts.mockResolvedValue({ items: [account(next.accountId)] });
  view.rerender(next); await flush();
  await act(async () => { finish({ items: [target('recipient-a', false)] }); });
  expect(view.result.current.status).toBe('connected');
  expect(view.result.current.account?.id).toBe(next.accountId);
});

it('shares confirmed status with the aggregate settings view without letting settings probe', async () => {
  const { result } = renderHook(() => ({
    task: useWeChatNotificationStatus(props),
    settings: useWeChatNotificationStatus({ enabled: true, aggregate: true }),
    unconfiguredTask: useWeChatNotificationStatus({ enabled: true, accountId: 'another-account' }),
  })); await flush();
  expect(result.current.task.status).toBe('connected');
  expect(result.current.settings.status).toBe('connected');
  expect(result.current.unconfiguredTask.status).toBe('unconfigured');
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  mocks.targets.mockResolvedValue({ items: [target('recipient-a', false)] });
  await act(async () => { await checkWeChatNotificationTarget({ account_id: 'chosen', recipient_id: 'recipient-a' }); });
  expect(result.current.task.status).toBe('pending');
  expect(result.current.settings.status).toBe('pending');
  expect(result.current.unconfiguredTask.status).toBe('unconfigured');
  expect(mocks.targets).toHaveBeenCalledTimes(2);
});

it('deduplicates concurrent checks for the same confirmed recipient', async () => {
  const { result } = renderHook(() => ({
    first: useWeChatNotificationStatus(props), second: useWeChatNotificationStatus(props),
  })); await flush();
  expect(mocks.targets).toHaveBeenCalledTimes(1);
  expect(mocks.accounts).toHaveBeenCalledTimes(1);
  expect(result.current.first.status).toBe('connected');
  expect(result.current.second.status).toBe('connected');
});

it('stops checking when disabled without displaying a late failed result', async () => {
  let finish!: (value: { items: ReturnType<typeof target>[] }) => void;
  mocks.targets.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  const view = renderHook(value => useWeChatNotificationStatus(value), { initialProps: props }); await flush();
  expect(view.result.current.status).toBe('checking');
  view.rerender({ ...props, enabled: false });
  await act(async () => { finish({ items: [target('recipient-a', false)] }); });
  await act(async () => { window.dispatchEvent(new Event('focus')); await vi.advanceTimersByTimeAsync(40 * 60_000); });
  expect(view.result.current.status).toBeUndefined();
  expect(mocks.targets).toHaveBeenCalledTimes(1);
});
