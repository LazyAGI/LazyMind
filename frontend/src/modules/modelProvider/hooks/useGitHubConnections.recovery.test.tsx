import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { useGitHubConnections } from './useGitHubConnections';

const mocks = vi.hoisted(() => ({
  start: vi.fn(), poll: vi.fn(), getAttempt: vi.fn(), cancelAttempt: vi.fn(), cancel: vi.fn(),
  list: vi.fn(), patch: vi.fn(), remove: vi.fn(), owner: 'owner-a', login: 'fixture-login-a', legacy: false,
}));
vi.mock('@/components/auth', () => ({
  AUTH_USER_CHANGE_EVENT: 'fixture-user-change', AUTH_LOGOUT_EVENT: 'fixture-logout',
  AgentAppsAuth: {
    getUserInfo: () => ({ userId: mocks.owner, username: mocks.owner, token: 'fixture-login-token',
      ...(mocks.legacy ? {} : { sessionId: mocks.login }) }),
    getSessionIdentity: () => JSON.stringify([mocks.legacy ? 'fixture-login-token' : mocks.login, mocks.owner, '',
      'http://fixture.local/api/authservice/auth/refresh']),
  },
}));
vi.mock('@/modules/dataSource/api/clients', () => ({ dataSourceCloudOauthApi: {
  startGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsPost: mocks.start,
  getGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdGet: mocks.poll,
  cancelGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdDelete: mocks.cancel,
  getGithubCliAttemptApiAuthserviceV1CloudGithubCliAttemptsAttemptIdGet: mocks.getAttempt,
  cancelGithubCliAttemptApiAuthserviceV1CloudGithubCliAttemptsAttemptIdDelete: mocks.cancelAttempt,
  listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.list,
  patchConnectionApiAuthserviceV1CloudConnectionsConnectionIdPatch: mocks.patch,
  deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete: mocks.remove,
} }));

const snapshot = (status: string, extra = {}) => ({ data: {
  session_id: 'fixture-session', status, expires_at: new Date(Date.now() + 900_000).toISOString(), ...extra,
} });
const waiting = () => snapshot('WAITING_USER', {
  user_code: 'ABCD-EFGH', verification_uri: 'https://github.com/login/device',
});
const storage = () => Array.from({ length: sessionStorage.length }, (_, n) => sessionStorage.getItem(sessionStorage.key(n)!)).join('\n');
const mount = () => renderHook(() => useGitHubConnections(vi.fn()));
beforeEach(() => {
  vi.resetAllMocks(); mocks.owner = 'owner-a'; mocks.login = 'fixture-login-a'; mocks.legacy = false; sessionStorage.clear();
  vi.stubGlobal('crypto', webcrypto);
  mocks.start.mockResolvedValue(waiting()); mocks.poll.mockResolvedValue(waiting());
  mocks.getAttempt.mockResolvedValue(waiting()); mocks.cancelAttempt.mockResolvedValue(snapshot('CANCELLED'));
  mocks.cancel.mockResolvedValue(snapshot('CANCELLED')); mocks.list.mockResolvedValue({ data: { items: [] } });
  Object.assign(window, { lazymindDesktop: { openGitHubCLIAuthorization: vi.fn().mockResolvedValue({ opened: true }) } });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); sessionStorage.clear(); delete (window as unknown as Record<string, unknown>).lazymindDesktop; });

it('explicit new web authorization does not reuse or delete a pending manual record', async () => {
  mocks.list.mockResolvedValue({ data: { items: [{ provider: 'github', connection_id: 'fixture-manual', status: 'PENDING', token_source: '' }] } });
  const { result } = mount();
  await act(async () => { await result.current.refresh(); await result.current.start('', true); });
  expect(mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody).toMatchObject({ force_web: true, reauthorize_connection_id: '' });
  expect(mocks.remove).not.toHaveBeenCalled();
});

it.each([502, 503, 504])('distinguishes gateway failure %s and keeps the same attempt for retry', async status => {
  mocks.start.mockRejectedValueOnce({ response: { status, data: { message: 'Authorization service unavailable' } } });
  const { result } = mount();
  await act(async () => result.current.start());
  expect(result.current.notice).toBe('serviceUnavailable');
  expect(result.current.retryCreate).toBe(true);
  const original = mocks.start.mock.calls[0][0];
  await act(async () => result.current.retry());
  expect(mocks.start).toHaveBeenCalledTimes(2);
  expect(mocks.start.mock.calls[1][0]).toEqual(original);
});

it('cancels an unknown create result by attempt without repeating create', async () => {
  mocks.start.mockRejectedValueOnce({ code: 'ECONNABORTED' });
  const { result } = mount();
  await act(async () => result.current.start());
  const attempt = mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody.attempt_id;
  await act(async () => result.current.cancel());
  expect(mocks.cancelAttempt).toHaveBeenCalledWith({ attemptId: attempt }, expect.anything());
  expect(mocks.start).toHaveBeenCalledTimes(1);
  expect(result.current.retryCreate).toBe(false);
  expect(result.current.session?.status).toBe('CANCELLED');
  expect(storage()).not.toContain(attempt);
});

it('keeps recovery available when cancellation cannot be confirmed', async () => {
  mocks.start.mockRejectedValueOnce({ code: 'ECONNABORTED' });
  mocks.cancelAttempt.mockRejectedValueOnce({ response: { status: 503 } });
  const { result } = mount();
  await act(async () => result.current.start());
  await act(async () => result.current.cancel());
  expect(result.current.notice).toBe('cancelFailed');
  expect(result.current.retryCreate).toBe(true);
  expect(mocks.start).toHaveBeenCalledTimes(1);
});

it('restores a waiting flow after remount using a read, without another login', async () => {
  const first = mount();
  await act(async () => first.result.current.start());
  const attempt = mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody.attempt_id;
  expect(storage()).toContain(attempt);
  expect(storage()).not.toContain('ABCD-EFGH');
  expect(storage()).not.toContain('fixture-login-token');
  first.unmount();
  const next = mount();
  await waitFor(() => expect(mocks.getAttempt).toHaveBeenCalledWith({ attemptId: attempt }, expect.anything()));
  await waitFor(() => expect(next.result.current.session?.status).toBe('WAITING_USER'));
  expect(mocks.start).toHaveBeenCalledTimes(1);
});

it('a missing attempt after service restart never automatically starts a new authorization', async () => {
  const first = mount(); await act(async () => first.result.current.start()); first.unmount();
  mocks.getAttempt.mockRejectedValue({ response: { status: 404, data: { code: 1001202 } } });
  const next = mount();
  await waitFor(() => expect(mocks.getAttempt).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(next.result.current.active).toBe(false));
  expect(mocks.start).toHaveBeenCalledTimes(1);
  expect(next.result.current.session?.user_code).toBeUndefined();
});

it('does not restore another signed-in users attempt', async () => {
  const first = mount(); await act(async () => first.result.current.start()); first.unmount();
  mocks.owner = 'owner-b';
  const next = mount();
  await act(async () => {});
  expect(mocks.getAttempt).not.toHaveBeenCalled();
  expect(next.result.current.active).toBe(false);
  expect(mocks.start).toHaveBeenCalledTimes(1);
});

it('ignores a late create response after the login identity changes', async () => {
  let respond!: (value: unknown) => void;
  mocks.start.mockImplementation(() => new Promise(resolve => { respond = resolve; }));
  const { result } = mount();
  act(() => { void result.current.start(); });
  act(() => { mocks.owner = 'owner-b'; window.dispatchEvent(new Event('fixture-user-change')); });
  await act(async () => respond(waiting()));
  expect(result.current.session?.user_code).toBeUndefined();
  expect(result.current.active).toBe(false);
});

it('honors a completed save returned while cancelling an uncertain request', async () => {
  mocks.start.mockRejectedValueOnce({ code: 'ECONNABORTED' });
  mocks.cancelAttempt.mockResolvedValue(snapshot('COMPLETED', { connection_id: 'fixture-cli' }));
  mocks.list.mockResolvedValue({ data: { items: [{ provider: 'github', connection_id: 'fixture-cli', status: 'ACTIVE' }] } });
  const { result } = mount(); await act(async () => result.current.start());
  await act(async () => result.current.cancel());
  expect(result.current.session?.status).toBe('COMPLETED');
  expect(result.current.accounts.map(a => a.connection_id)).toContain('fixture-cli');
  expect(result.current.notice).not.toBe('cancelled');
});

it('unavailable browser storage does not break authorization or cancellation', async () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('fixture storage blocked'); });
  const { result } = mount();
  await act(async () => result.current.start());
  expect(result.current.session?.status).toBe('WAITING_USER');
  await act(async () => result.current.cancel());
  expect(result.current.session?.status).toBe('CANCELLED');
});

it('logout clears the displayed device code and prevents further polling', async () => {
  const { result } = mount();
  await act(async () => result.current.start());
  act(() => window.dispatchEvent(new Event('fixture-logout')));
  expect(result.current.session?.user_code).toBeUndefined();
  expect(result.current.active).toBe(false);
  expect(storage()).not.toContain('fixture-session');
});

it('a new login by the same user does not resume the previous login attempt', async () => {
  const first = mount(); await act(async () => first.result.current.start()); first.unmount();
  mocks.login = 'fixture-login-b';
  const next = mount();
  await act(async () => {});
  expect(mocks.getAttempt).not.toHaveBeenCalled();
  expect(next.result.current.active).toBe(false);
});

it('ignores a recovery response that arrives after the same user logs in again', async () => {
  const first = mount(); await act(async () => first.result.current.start()); first.unmount();
  let resolve!: (value: unknown) => void;
  mocks.getAttempt.mockImplementation(() => new Promise(done => { resolve = done; }));
  const next = mount();
  await waitFor(() => expect(mocks.getAttempt).toHaveBeenCalledTimes(1));
  act(() => { mocks.login = 'fixture-login-b'; window.dispatchEvent(new Event('fixture-user-change')); });
  await act(async () => resolve(waiting()));
  expect(next.result.current.session?.user_code).toBeUndefined();
  expect(next.result.current.active).toBe(false);
});

it('legacy identities containing a token can recover without persisting the raw identity', async () => {
  mocks.legacy = true;
  const first = mount(); await act(async () => first.result.current.start());
  const attempt = mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody.attempt_id;
  expect(storage()).toContain(attempt);
  expect(storage()).not.toContain('fixture-login-token');
  expect(storage()).not.toContain('ABCD-EFGH');
  first.unmount();
  const next = mount();
  await waitFor(() => expect(next.result.current.session?.status).toBe('WAITING_USER'));
  expect(mocks.start).toHaveBeenCalledTimes(1);
});

