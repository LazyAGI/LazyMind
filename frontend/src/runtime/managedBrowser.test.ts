import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';

const user = vi.hoisted(() => vi.fn());
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getUserInfo: user }, AUTH_USER_CHANGE_EVENT: 'test-user-change' }));
vi.mock('./apiBase', () => ({ getApiBaseUrl: () => 'http://127.0.0.1:8090' }));
import { hasManagedBrowser, openManagedBrowser, selectManagedBrowser, startManagedBrowserSync, syncManagedBrowser } from './managedBrowser';

describe('Desktop browser connection', () => {
  beforeEach(() => { user.mockReset(); vi.useFakeTimers(); });
  afterEach(() => { Reflect.deleteProperty(window, 'lazymindDesktop'); vi.useRealTimers(); });
  function install() {
    const desktop = {
      browserSessionSet: vi.fn().mockResolvedValue({ state: 'connected' }),
      browserStatus: vi.fn().mockResolvedValue({ state: 'connected' }),
      browserOpen: vi.fn().mockResolvedValue({}),
      browserSelect: vi.fn().mockResolvedValue({ state: 'connecting', engine: 'edge', edgeAvailable: true }),
    };
    Object.defineProperty(window, 'lazymindDesktop', { configurable: true, value: desktop });
    return desktop;
  }
  it('does nothing outside Desktop', () => {
    expect(hasManagedBrowser()).toBe(false);
    startManagedBrowserSync()();
    expect(user).not.toHaveBeenCalled();
  });
  it('connects with the signed-in account without a manual pairing code or refresh token', async () => {
    const desktop = install();
    user.mockReturnValue({ userId: 'alice', token: 'token' });
    await openManagedBrowser('https://example.com');
    expect(desktop.browserSessionSet).toHaveBeenCalledWith({
      user_id: 'alice', access_token: 'token', server_url: 'http://127.0.0.1:8090',
    });
    expect(desktop.browserOpen).toHaveBeenCalledWith('https://example.com');
  });
  it('clears the desktop account when logging out and removes listeners on unmount', async () => {
    const desktop = install();
    user.mockReturnValue({ userId: 'alice', token: 'token' });
    const stop = startManagedBrowserSync();
    user.mockReturnValue(null);
    window.dispatchEvent(new Event('test-user-change'));
    expect(desktop.browserSessionSet).toHaveBeenLastCalledWith(null);
    stop();
    desktop.browserSessionSet.mockClear();
    window.dispatchEvent(new Event('test-user-change'));
    await vi.advanceTimersByTimeAsync(60000);
    expect(desktop.browserSessionSet).not.toHaveBeenCalled();
  });
  it('reports failed setup rather than opening an unbound window', async () => {
    const desktop = install();
    desktop.browserSessionSet.mockRejectedValue(new Error('offline'));
    await expect(openManagedBrowser('https://example.com')).rejects.toThrow('offline');
    expect(desktop.browserOpen).not.toHaveBeenCalled();
    await expect(syncManagedBrowser()).rejects.toThrow('offline');
  });
  it('synchronizes the account before selecting Edge and returns its status', async () => {
    const desktop = install();
    user.mockReturnValue({ userId: 'alice', token: 'token' });
    expect(await selectManagedBrowser('edge')).toEqual({ state: 'connecting', engine: 'edge', edgeAvailable: true });
    expect(desktop.browserSelect).toHaveBeenCalledWith('edge');
    expect(desktop.browserSessionSet.mock.invocationCallOrder[0]).toBeLessThan(desktop.browserSelect.mock.invocationCallOrder[0]);
    desktop.browserSessionSet.mockRejectedValue(new Error('offline'));
    desktop.browserSelect.mockClear();
    await expect(selectManagedBrowser('builtin')).rejects.toThrow('offline');
    expect(desktop.browserSelect).not.toHaveBeenCalled();
  });
});
