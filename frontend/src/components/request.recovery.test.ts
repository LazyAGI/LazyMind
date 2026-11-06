import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import axios, { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { AgentAppsAuth } from './auth';
import { axiosInstance, handleError } from './request';
import { clearLocalAssistantSession } from '@/runtime/assistantSession';

const mocks = vi.hoisted(() => ({ restore: vi.fn(), local: true }));
vi.mock('antd', () => ({ message: { error: vi.fn(), warning: vi.fn() } }));
vi.mock('@/i18n', () => ({ default: { t: (s: string) => s, exists: () => false, language: 'en' } }));
vi.mock('@/runtime/assistantSession', () => ({ clearLocalAssistantSession: vi.fn() }));
vi.mock('@/runtime/localSession', () => ({ isLocalSessionEnabled: () => mocks.local, localSessionInitialized: true, ensureLocalSession: vi.fn(), restoreLocalSessionAndGetToken: mocks.restore }));
const user = { username: 'A', userId: 'A', token: 'old', refreshToken: 'old-refresh' };
const reject401 = (config: InternalAxiosRequestConfig) => new AxiosError('expired', 'ERR_BAD_REQUEST', config, {}, { status: 401, statusText: 'Unauthorized', headers: {}, config, data: {} });
function oldRequest(retried = true) {
  return { url: '/api/core/workflow-sessions', headers: new AxiosHeaders({ authorization: 'Bearer old' }),
    _authSession: AgentAppsAuth.getSessionIdentity(), _retry: retried, _localSessionRetry: retried } as unknown as InternalAxiosRequestConfig;
}
beforeEach(() => {
  localStorage.clear(); vi.clearAllMocks(); mocks.local = true; mocks.restore.mockReset();
  AgentAppsAuth.setUserInfo(user);
  axiosInstance.defaults.adapter = async config => ({ status: 200, statusText: 'OK', headers: {}, config, data: {} });
});
afterEach(() => vi.restoreAllMocks());

it('replays an old exhausted 401 with the renewed same-account token without logout', async () => {
  const request = oldRequest();
  AgentAppsAuth.replaceLocalSession({ ...user, token: 'renewed', refreshToken: 'renewed-refresh' });
  const logout = vi.spyOn(AgentAppsAuth, 'logout').mockResolvedValue();
  const response = await handleError(reject401(request));
  expect(response.config.headers.get('authorization')).toBe('Bearer renewed');
  expect(AgentAppsAuth.getAccessToken()).toBe('renewed');
  expect(logout).not.toHaveBeenCalled();
  expect(clearLocalAssistantSession).not.toHaveBeenCalled();
});

it('uses a newer credential when an older refresh fails after local recovery', async () => {
  const request = oldRequest(false);
  let fail!: (error: Error) => void;
  vi.spyOn(axios, 'create').mockReturnValue({ post: () => new Promise((_resolve, reject) => { fail = reject; }) } as never);
  const logout = vi.spyOn(AgentAppsAuth, 'logout').mockResolvedValue();
  const result = handleError(reject401(request));
  AgentAppsAuth.replaceLocalSession({ ...user, token: 'renewed', refreshToken: 'renewed-refresh' });
  fail(new Error('old refresh failed'));
  expect((await result).config.headers.get('authorization')).toBe('Bearer renewed');
  expect(mocks.restore).not.toHaveBeenCalled();
  expect(logout).not.toHaveBeenCalled();
});

it.each([true, false])('does not log out on a temporary refresh failure (local=%s)', async local => {
  mocks.local = local;
  vi.spyOn(axios, 'create').mockReturnValue({ post: vi.fn().mockRejectedValue(new Error('temporary network failure')) } as never);
  mocks.restore.mockRejectedValue(new Error('runtime restarting'));
  const logout = vi.spyOn(AgentAppsAuth, 'logout').mockResolvedValue();
  await expect(handleError(reject401(oldRequest(false)))).rejects.toBeInstanceOf(Error);
  expect(AgentAppsAuth.getAccessToken()).toBe('old');
  expect(logout).not.toHaveBeenCalled();
  expect(clearLocalAssistantSession).not.toHaveBeenCalled();
});

it('does not replay an old response into a replacement account', async () => {
  const request = oldRequest();
  AgentAppsAuth.setUserInfo({ ...user, userId: 'B', token: 'B' });
  const expire = vi.spyOn(AgentAppsAuth, 'expireSession').mockResolvedValue();
  await expect(handleError(reject401(request))).rejects.toBeInstanceOf(Error);
  expect(expire).not.toHaveBeenCalled();
  expect(AgentAppsAuth.getAccessToken()).toBe('B');
});

it('bounds newer-credential retries without expiring a still newer login', async () => {
  const request = oldRequest();
  AgentAppsAuth.replaceLocalSession({ ...user, token: 'second' });
  let requests = 0;
  axiosInstance.defaults.adapter = async config => {
    requests++;
    AgentAppsAuth.replaceLocalSession({ ...user, token: 'third' });
    throw reject401(config);
  };
  const logout = vi.spyOn(AgentAppsAuth, 'logout').mockResolvedValue();
  await expect(handleError(reject401(request))).rejects.toThrow('STALE_AUTH_CREDENTIALS');
  expect(requests).toBe(1);
  expect(AgentAppsAuth.getAccessToken()).toBe('third');
  expect(logout).not.toHaveBeenCalled();
});

it('expires only a confirmed current credential after bounded recovery', async () => {
  const expected = AgentAppsAuth.getSessionSnapshot();
  const expire = vi.spyOn(AgentAppsAuth, 'expireSession').mockResolvedValue();
  await expect(handleError(reject401(oldRequest()))).rejects.toBeInstanceOf(Error);
  expect(expire).toHaveBeenCalledWith(expected);
});

it('does not replace an explicit foreign authorization header with LazyMind credentials', async () => {
  const refresh = vi.spyOn(AgentAppsAuth, 'refreshAccessToken').mockResolvedValue('must-not-be-sent');
  let calls = 0;
  axiosInstance.defaults.adapter = async config => { calls++; throw reject401(config); };
  await expect(axiosInstance.get('https://provider.example/private', { headers: { authorization: 'Bearer provider-token' } })).rejects.toBeInstanceOf(Error);
  expect(calls).toBe(1);
  expect(refresh).not.toHaveBeenCalled();
});

it('invalidates only the failed credential snapshot after a genuine remote refresh rejection', async () => {
  mocks.local = false;
  const request = oldRequest(false);
  vi.spyOn(axios, 'create').mockReturnValue({ post: vi.fn().mockRejectedValue(reject401(request)) } as never);
  const expire = vi.spyOn(AgentAppsAuth, 'expireSession').mockResolvedValue();
  await expect(handleError(reject401(request))).rejects.toBeInstanceOf(Error);
  expect(expire).toHaveBeenCalledWith(AgentAppsAuth.getSessionSnapshot());
});
