import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import axios, { AxiosError, type InternalAxiosRequestConfig } from 'axios';
import { AgentAppsAuth } from './auth';
import { axiosInstance } from './request';
vi.mock('antd', () => ({ message: { error: vi.fn(), warning: vi.fn() } }));
vi.mock('@/i18n', () => ({ default: { t: (s: string) => s, language: 'en' } }));
vi.mock('@/runtime/assistantSession', () => ({ clearLocalAssistantSession: vi.fn() }));
vi.mock('@/runtime/localSession', () => ({ isLocalSessionEnabled: () => false, localSessionInitialized: true, ensureLocalSession: vi.fn(), restoreLocalSessionAndGetToken: vi.fn() }));
const user = (id: string) => ({ username: id, userId: id, tenantId: id, token: id, refreshToken: id + '-refresh' });
const tick = async () => { for (let i = 0; i < 40; i++) await Promise.resolve(); };
const unauthorized = (config: InternalAxiosRequestConfig) => new AxiosError('expired', 'ERR_BAD_REQUEST', config, {}, { status: 401, statusText: '', headers: {}, config, data: {} });
beforeEach(() => { localStorage.clear(); });
afterEach(() => vi.restoreAllMocks());
it('late unauthorized response cannot log out a replacement account', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  let fail!: () => void;
  axiosInstance.defaults.adapter = config => new Promise((_resolve, reject) => { fail = () => reject(unauthorized(config)); });
  const logout = vi.spyOn(AgentAppsAuth, 'logout').mockResolvedValue();
  const result = axiosInstance.get('/old').catch(error => error);
  await tick();
  AgentAppsAuth.setUserInfo(user('B'));
  fail(); await result;
  expect(logout).not.toHaveBeenCalled();
  expect(AgentAppsAuth.getUserInfo()?.userId).toBe('B');
});
it('concurrent account refreshes never share a global retry token or log out the new account', async () => {
  const pending: Record<string, (value: unknown) => void> = {};
  const post = vi.fn((_url, body) => new Promise(resolve => { pending[body.refresh_token] = resolve; }));
  vi.spyOn(axios, 'create').mockReturnValue({ post } as never);
  const logout = vi.spyOn(AgentAppsAuth, 'logout').mockResolvedValue();
  const retried: InternalAxiosRequestConfig[] = [];
  axiosInstance.defaults.adapter = async config => {
    if (!(config as any)._retry) throw unauthorized(config);
    retried.push(config);
    return { status: 200, statusText: '', headers: {}, config, data: {} };
  };
  AgentAppsAuth.setUserInfo(user('A'));
  const old = axiosInstance.get('/old').catch(error => error);
  await tick();
  AgentAppsAuth.setUserInfo(user('B'));
  const current = axiosInstance.get('/new').catch(error => error);
  await tick();
  expect(post).toHaveBeenCalledTimes(2);
  pending['A-refresh']({ data: { access_token: 'rotated-A' } });
  pending['B-refresh']({ data: { access_token: 'rotated-B' } });
  expect(await old).toBeInstanceOf(Error);
  await current;
  expect(retried).toHaveLength(1);
  expect(retried[0].url).toBe('/new');
  expect(retried[0].headers.authorization).toBe('Bearer rotated-B');
  expect(retried[0].headers['X-User-Id']).toBe('B');
  expect(logout).not.toHaveBeenCalled();
});
it('late successful response is not handed to a replacement account consumer', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  let finish!: () => void;
  axiosInstance.defaults.adapter = config => new Promise(resolve => { finish = () => resolve({ status: 200, statusText: '', headers: {}, config, data: { user_id: 'A' } }); });
  const result = axiosInstance.get('/profile').catch(error => error);
  await tick(); AgentAppsAuth.setUserInfo(user('B')); finish();
  expect(await result).toBeInstanceOf(Error);
});
it('delivers an in-flight notification response after a profile-only update', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  let finish!: () => void;
  axiosInstance.defaults.adapter = config => new Promise(resolve => {
    finish = () => resolve({ status: 200, statusText: '', headers: {}, config, data: { items: [] } });
  });
  const result = axiosInstance.get('/api/core/task-center/desktop-notifications');
  await tick();
  AgentAppsAuth.updateUserInfo({ displayName: 'New name' });
  finish();
  expect((await result).data).toEqual({ items: [] });
});

it('keeps same-account requests alive when local credentials are renewed', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  let finish!: () => void;
  axiosInstance.defaults.adapter = config => new Promise(resolve => {
    finish = () => resolve({ status: 200, statusText: '', headers: {}, config, data: { items: [] } });
  });
  const result = axiosInstance.get('/api/authservice/v1/cloud/connections');
  await tick();
  AgentAppsAuth.replaceLocalSession({ ...user('A'), token: 'renewed-A' });
  finish();
  expect((await result).data).toEqual({ items: [] });
  expect(AgentAppsAuth.getAccessToken()).toBe('renewed-A');
});
