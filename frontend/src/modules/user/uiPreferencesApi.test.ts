import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchUserUiPreferences, patchUserUiPreferences } from './uiPreferencesApi';

const api = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), identity: 'first' }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: {} }));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getSessionIdentity: () => api.identity } }));
vi.mock('@/api/generated/core-client', () => ({
  Configuration: class {},
  UserApiFactory: () => ({ apiCoreUserUiPreferencesGet: api.get, apiCoreUserUiPreferencesPatch: api.patch }),
}));

function deferred() {
  let resolve!: (value: { data: unknown }) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<{ data: unknown }>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

beforeEach(() => { vi.clearAllMocks(); api.identity = 'first'; });

describe('concurrent preference reads', () => {
  it('shares only in-flight reads and fetches again after completion', async () => {
    const response = deferred();
    api.get.mockReturnValueOnce(response.promise).mockResolvedValue({ data: { developer_mode_active: false } });
    const first = fetchUserUiPreferences();
    const second = fetchUserUiPreferences();
    expect(api.get).toHaveBeenCalledTimes(1);
    response.resolve({ data: { data: { developer_mode_active: true } } });
    expect(await first).toEqual({ developer_mode_active: true });
    expect(await second).toEqual({ developer_mode_active: true });
    expect(await fetchUserUiPreferences()).toEqual({ developer_mode_active: false });
    expect(api.get).toHaveBeenCalledTimes(2);
  });

  it('propagates failures and allows a retry', async () => {
    api.get.mockRejectedValueOnce(new Error('unavailable')).mockResolvedValue({ data: {} });
    const requests = [fetchUserUiPreferences(), fetchUserUiPreferences()];
    expect((await Promise.allSettled(requests)).map(result => result.status)).toEqual(['rejected', 'rejected']);
    await fetchUserUiPreferences();
    expect(api.get).toHaveBeenCalledTimes(2);
  });

  it('isolates sessions and callers with their own cancellation options', async () => {
    const response = deferred();
    api.get.mockReturnValue(response.promise);
    const first = fetchUserUiPreferences();
    api.identity = 'second';
    const second = fetchUserUiPreferences();
    const signal = new AbortController().signal;
    const custom = fetchUserUiPreferences({ signal });
    expect(api.get).toHaveBeenCalledTimes(3);
    expect(api.get).toHaveBeenLastCalledWith({ signal });
    response.resolve({ data: {} });
    await Promise.all([first, second, custom]);
  });

  it('does not reuse a read started before a preference update', async () => {
    const old = deferred();
    api.get.mockReturnValueOnce(old.promise).mockResolvedValue({ data: { developer_mode_active: true } });
    api.patch.mockResolvedValue({ data: { developer_mode_active: true } });
    const pending = fetchUserUiPreferences();
    await patchUserUiPreferences({ developer_mode_active: true });
    expect(await fetchUserUiPreferences()).toEqual({ developer_mode_active: true });
    expect(api.get).toHaveBeenCalledTimes(2);
    old.resolve({ data: { developer_mode_active: false } });
    await pending;
  });
});
