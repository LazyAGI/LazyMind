import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useChannelConnection } from './useChannelConnection';
import type { ConnectionSession } from '../api';

const mocks = vi.hoisted(() => ({
  listChannelAccounts: vi.fn(), createConnectionSession: vi.fn(),
  getConnectionSession: vi.fn(), cancelConnectionSession: vi.fn(),
  refreshConnectionSession: vi.fn(), submitConnectionChallenge: vi.fn(),
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
    allowed_actions: ['waiting_scan', 'scanned', 'confirming', 'verification_required'].includes(status) ? ['cancel'] : [],
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

  it('cleans up a late QR response before a remounted panel can start another scan', async () => {
    let finishCreate!: (value: ConnectionSession) => void;
    let finishCancel!: () => void;
    mocks.createConnectionSession.mockImplementationOnce(() => new Promise<ConnectionSession>(resolve => {
      finishCreate = resolve;
    })).mockResolvedValueOnce({ ...session('waiting_scan'), id: 'new-session' });
    mocks.cancelConnectionSession.mockImplementationOnce(() => new Promise<void>(resolve => {
      finishCancel = resolve;
    }));
    const first = renderHook(() => useChannelConnection('wechat'));
    let firstScan!: Promise<void>;
    await act(async () => { firstScan = first.result.current.startScan(); });
    expect(mocks.createConnectionSession).toHaveBeenCalledTimes(1);
    first.unmount();
    const second = renderHook(() => useChannelConnection('wechat'));
    let secondScan!: Promise<void>;
    act(() => { secondScan = second.result.current.startScan({ accountId: 'another-account' }); });
    await act(async () => { finishCreate(session('waiting_scan')); });
    expect(mocks.cancelConnectionSession).toHaveBeenCalledWith('session', { silentError: true });
    expect(mocks.createConnectionSession).toHaveBeenCalledTimes(1);
    await act(async () => { finishCancel(); await firstScan; await secondScan; });
    expect(mocks.createConnectionSession).toHaveBeenCalledTimes(2);
    expect(second.result.current.session?.id).toBe('new-session');
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it('keeps a late Feishu QR available for background provisioning', async () => {
    let finishCreate!: (value: ConnectionSession) => void;
    mocks.createConnectionSession.mockReturnValueOnce(new Promise<ConnectionSession>(resolve => {
      finishCreate = resolve;
    }));
    const view = renderHook(() => useChannelConnection('feishu'));
    let scan!: Promise<void>;
    await act(async () => { scan = view.result.current.startScan({ createNew: true }); });
    view.unmount();
    await act(async () => { finishCreate({ ...session('waiting_scan'), provider: 'feishu' }); await scan; });
    expect(mocks.cancelConnectionSession).not.toHaveBeenCalled();
  });

  it('can retry cleanup after a transient cancellation failure', async () => {
    mocks.createConnectionSession.mockResolvedValueOnce(session('waiting_scan'))
      .mockRejectedValueOnce({ response: { status: 409 } })
      .mockResolvedValueOnce(session('connected'));
    mocks.cancelConnectionSession.mockRejectedValueOnce(new Error('network unavailable'))
      .mockResolvedValueOnce(undefined);
    const view = renderHook(() => useChannelConnection('wechat'));
    await act(() => view.result.current.startScan());
    await act(() => view.result.current.startScan());
    await act(() => view.result.current.startScan());
    expect(mocks.cancelConnectionSession).toHaveBeenCalledTimes(2);
    expect(view.result.current.session?.status).toBe('connected');
  });

  it('does not show late creation errors after leaving the panel', async () => {
    let failCreate!: (error: unknown) => void;
    mocks.createConnectionSession.mockReturnValueOnce(new Promise<ConnectionSession>((_resolve, reject) => {
      failCreate = reject;
    }));
    const view = renderHook(() => useChannelConnection('wechat'));
    let scan!: Promise<void>;
    await act(async () => { scan = view.result.current.startScan(); });
    view.unmount();
    await act(async () => { failCreate({ response: { status: 409 } }); await scan; });
    expect(mocks.createConnectionSession).toHaveBeenCalledWith('wechat', expect.objectContaining({ silentError: true }));
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it('still reports a creation failure in the active panel', async () => {
    mocks.createConnectionSession.mockRejectedValueOnce({ response: { status: 409 } });
    const view = renderHook(() => useChannelConnection('wechat'));
    await act(() => view.result.current.startScan());
    expect(mocks.error).toHaveBeenCalledWith('请求冲突');
    expect(view.result.current.sessionStarting).toBe(false);
  });

  it.each(['refresh', 'challenge'] as const)('cleans up a late %s response once when leaving', async operation => {
    let finish!: (value: ConnectionSession) => void;
    const request = operation === 'refresh' ? mocks.refreshConnectionSession : mocks.submitConnectionChallenge;
    request.mockReturnValueOnce(new Promise<ConnectionSession>(resolve => { finish = resolve; }));
    mocks.createConnectionSession.mockResolvedValueOnce(session(operation === 'refresh' ? 'expired' : 'verification_required'));
    const view = renderHook(() => useChannelConnection('wechat'));
    await act(() => view.result.current.startScan());
    act(() => view.result.current.setChallengeValue('1234'));
    let action!: Promise<void>;
    await act(async () => {
      action = operation === 'refresh' ? view.result.current.refreshQr() : view.result.current.submitChallenge();
    });
    view.unmount();
    await act(async () => { finish(session('confirming')); await action; });
    expect(mocks.cancelConnectionSession).toHaveBeenCalledTimes(1);
    expect(mocks.cancelConnectionSession).toHaveBeenCalledWith('session', { silentError: true });
    expect(mocks.error).not.toHaveBeenCalled();
  });
});
