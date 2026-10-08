import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useChannelConnection } from './useChannelConnection';
import type { ConnectionSession } from '../api';

const mocks = vi.hoisted(() => ({
  listChannelAccounts: vi.fn(), createConnectionSession: vi.fn(),
  getConnectionSession: vi.fn(), cancelConnectionSession: vi.fn(),
  error: vi.fn(), success: vi.fn(),
}));
vi.mock('../api', () => mocks);
vi.mock('antd', () => ({ message: { error: mocks.error, success: mocks.success } }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => '请求冲突' }));
vi.mock('@/modules/settings/SettingsNavigationGuard', () => ({ useSettingsDraft: vi.fn() }));
vi.mock('react-i18next', () => {
  const t = (key: string) => key;
  return { useTranslation: () => ({ t }) };
});

function session(status: ConnectionSession['status']): ConnectionSession {
  return {
    id: 'session', provider: 'wechat', mode: 'qr_code', status, revision: 1,
    allowed_actions: ['waiting_scan', 'scanned', 'confirming'].includes(status) ? ['cancel'] : [],
    message: '', poll_after_ms: 1000,
  } as ConnectionSession;
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.listChannelAccounts.mockResolvedValue({ items: [] });
  mocks.cancelConnectionSession.mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe('connection session cleanup during navigation', () => {
  it.each(['connected', 'expired', 'failed', 'canceled'] as const)(
    'does not cancel a %s session when leaving or starting another scan', async status => {
      mocks.createConnectionSession.mockResolvedValue(session(status));
      const view = renderHook(() => useChannelConnection('wechat'));
      await act(() => view.result.current.startScan());
      await act(() => view.result.current.startScan());
      view.unmount();
      expect(mocks.cancelConnectionSession).not.toHaveBeenCalled();
    },
  );

  it('uses the latest polled terminal status when leaving', async () => {
    vi.useFakeTimers();
    mocks.createConnectionSession.mockResolvedValue(session('waiting_scan'));
    mocks.getConnectionSession.mockResolvedValue(session('expired'));
    const view = renderHook(() => useChannelConnection('wecom'));
    await act(() => view.result.current.startScan());
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(view.result.current.session?.status).toBe('expired');
    view.unmount();
    expect(mocks.cancelConnectionSession).not.toHaveBeenCalled();
  });

  it('silently cancels an active scan if the server has already finished it', async () => {
    mocks.createConnectionSession.mockResolvedValue(session('waiting_scan'));
    mocks.cancelConnectionSession.mockRejectedValue({ response: { status: 409 } });
    const view = renderHook(() => useChannelConnection('wechat'));
    await act(() => view.result.current.startScan());
    view.unmount();
    await act(async () => {});
    expect(mocks.cancelConnectionSession).toHaveBeenCalledTimes(1);
    expect(mocks.cancelConnectionSession).toHaveBeenCalledWith('session', { silentError: true });
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it('does not cancel twice when switching during an explicit cancellation', async () => {
    mocks.createConnectionSession.mockResolvedValue(session('waiting_scan'));
    let finish!: () => void;
    mocks.cancelConnectionSession.mockReturnValue(new Promise<void>(resolve => { finish = resolve; }));
    const view = renderHook(() => useChannelConnection('wechat'));
    await act(() => view.result.current.startScan());
    let cancellation!: Promise<void>;
    act(() => { cancellation = view.result.current.cancelScan(); });
    view.unmount();
    await act(async () => { finish(); await cancellation; });
    expect(mocks.cancelConnectionSession).toHaveBeenCalledTimes(1);
    expect(mocks.cancelConnectionSession).toHaveBeenCalledWith('session', { silentError: true });
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it('preserves Feishu provisioning when switching away', async () => {
    mocks.createConnectionSession.mockResolvedValue({ ...session('confirming'), provider: 'feishu' });
    const view = renderHook(() => useChannelConnection('feishu'));
    await act(() => view.result.current.startScan());
    view.unmount();
    expect(mocks.cancelConnectionSession).not.toHaveBeenCalled();
  });
});
