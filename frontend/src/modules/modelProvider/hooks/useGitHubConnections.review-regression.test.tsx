import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { useGitHubConnections } from './useGitHubConnections';

const mocks = vi.hoisted(() => ({ start: vi.fn(), poll: vi.fn(), list: vi.fn(), getAttempt: vi.fn(), open: vi.fn(),
  owner: 'owner-a', login: 'login-a', authenticated: false }));
vi.mock('@/components/auth', () => ({
  AUTH_USER_CHANGE_EVENT: 'fixture-user-change', AUTH_LOGOUT_EVENT: 'fixture-logout',
  AgentAppsAuth: {
    getUserInfo: () => mocks.authenticated ? { userId: mocks.owner, sessionId: mocks.login } : null,
    getSessionIdentity: () => JSON.stringify([mocks.login, mocks.owner, '', 'fixture-auth-endpoint']),
  },
}));
vi.mock('@/modules/dataSource/api/clients', () => ({ dataSourceCloudOauthApi: {
  startGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsPost: mocks.start,
  getGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdGet: mocks.poll,
  listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.list,
  getGithubCliAttemptApiAuthserviceV1CloudGithubCliAttemptsAttemptIdGet: mocks.getAttempt,
} }));
const waiting = () => ({ data: { session_id: 'fixture-session', status: 'WAITING_USER',
  expires_at: new Date(Date.now() + 900_000).toISOString(), user_code: 'ABCD-EFGH',
  verification_uri: 'https://github.com/login/device' } });
beforeEach(() => {
  vi.useFakeTimers(); vi.resetAllMocks(); sessionStorage.clear(); mocks.owner = 'owner-a'; mocks.login = 'login-a';
  mocks.authenticated = false;
  mocks.start.mockResolvedValue(waiting()); mocks.poll.mockResolvedValue(waiting());
  mocks.getAttempt.mockResolvedValue(waiting()); mocks.open.mockResolvedValue({ opened: true });
  mocks.list.mockResolvedValue({ data: { items: [{ provider: 'github', status: 'ACTIVE', connection_id: 'fixture-old-owner' }] } });
  Object.assign(window, { lazymindDesktop: { openGitHubCLIAuthorization: mocks.open } });
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); delete (window as unknown as Record<string, unknown>).lazymindDesktop; });

it.each(['different-user', 'same-user-new-login'])('invalidates a cross-tab %s and ignores its late poll response', async change => {
  let respond!: (value: unknown) => void;
  mocks.poll.mockImplementation(() => new Promise(resolve => { respond = resolve; }));
  const { result } = renderHook(() => useGitHubConnections(vi.fn()));
  await act(async () => { await result.current.refresh(); await result.current.start(); });
  expect(result.current.session?.user_code).toBe('ABCD-EFGH');
  await act(async () => vi.advanceTimersByTimeAsync(1000));
  expect(mocks.poll).toHaveBeenCalledTimes(1);
  act(() => {
    if (change === 'different-user') mocks.owner = 'owner-b';
    mocks.login = 'login-b';
    const event = new StorageEvent('storage', { key: 'lazymind:user' });
    // The shared test setup replaces native Storage with an in-memory fixture.
    Object.defineProperty(event, 'storageArea', { value: localStorage });
    window.dispatchEvent(event);
  });
  expect(result.current.session?.user_code).toBeUndefined();
  expect(result.current.active).toBe(false);
  expect(result.current.accounts).toEqual([]);
  await act(async () => respond(waiting()));
  await act(async () => vi.advanceTimersByTimeAsync(5000));
  expect(result.current.session?.user_code).toBeUndefined();
  expect(mocks.poll).toHaveBeenCalledTimes(1);
});

it.each([502, 503, 504])('classifies poll HTTP %s as service unavailability while retaining the same session', async status => {
  mocks.poll.mockRejectedValueOnce({ response: { status, data: { message: 'Authorization service unavailable' } } });
  const { result } = renderHook(() => useGitHubConnections(vi.fn()));
  await act(async () => result.current.start());
  await act(async () => vi.advanceTimersByTimeAsync(1000));
  expect(result.current.notice).toBe('serviceUnavailable');
  expect(result.current.session?.session_id).toBe('fixture-session');
  await act(async () => vi.advanceTimersByTimeAsync(2000));
  expect(result.current.notice).toBe('');
  expect(result.current.session?.status).toBe('WAITING_USER');
  expect(mocks.start).toHaveBeenCalledTimes(1);
  expect(mocks.poll).toHaveBeenCalledTimes(2);
});

it('restores the device prompt without automatically opening a duplicate authorization page', async () => {
  vi.useRealTimers(); vi.stubGlobal('crypto', webcrypto); mocks.authenticated = true;
  const first = renderHook(() => useGitHubConnections(vi.fn()));
  await act(async () => first.result.current.start());
  expect(mocks.open).toHaveBeenCalledTimes(1);
  first.unmount();
  const next = renderHook(() => useGitHubConnections(vi.fn()));
  await waitFor(() => expect(next.result.current.session?.status).toBe('WAITING_USER'));
  expect(mocks.getAttempt).toHaveBeenCalledTimes(1);
  expect(mocks.start).toHaveBeenCalledTimes(1);
  expect(mocks.open).toHaveBeenCalledTimes(1);
});
