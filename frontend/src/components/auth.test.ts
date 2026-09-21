import { beforeEach, expect, it, vi } from 'vitest';
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from './auth';
const mocks = vi.hoisted(() => ({ post: vi.fn(), api: 'https://one/auth/refresh' }));
vi.mock('axios', () => ({ default: { create: () => ({ post: mocks.post }) } }));
vi.mock('@/runtime/apiBase', () => ({ authServiceApiUrl: () => mocks.api, coreApiUrl: () => '/core' }));
vi.mock('@/i18n', () => ({ default: { t: (s: string) => s } }));
vi.mock('@/modules/signin/utils/request', () => ({ logoutFromServer: vi.fn() }));
vi.mock('@/runtime/assistantSession', () => ({ clearLocalAssistantSession: vi.fn().mockResolvedValue(undefined) }));
const user = (name: string) => ({ username: name, userId: name, token: name + '-access', refreshToken: name + '-refresh', tenantId: name + '-tenant' });
beforeEach(() => { localStorage.clear(); mocks.post.mockReset(); mocks.api = 'https://one/auth/refresh'; });
it.each(['switch', 'clear-switch', 'backend'])('rejects stale refresh after %s without publishing old credentials', async mode => {
  AgentAppsAuth.setUserInfo(user('A'));
  let done!: (value: unknown) => void;
  mocks.post.mockReturnValue(new Promise(resolve => { done = resolve; }));
  const result = AgentAppsAuth.refreshAccessToken().catch(error => error);
  await Promise.resolve();
  if (mode === 'clear-switch') AgentAppsAuth.clearUserInfo();
  if (mode === 'backend') mocks.api = 'https://two/auth/refresh';
  else AgentAppsAuth.setUserInfo(user('B'));
  const changed = vi.fn(); window.addEventListener(AUTH_USER_CHANGE_EVENT, changed);
  done({ data: { access_token: 'late-A', refresh_token: 'late-refresh-A' } });
  expect(await result).toBeInstanceOf(Error);
  expect(AgentAppsAuth.getAuthHeaders().authorization).toBe(`Bearer ${mode === 'backend' ? 'A' : 'B'}-access`);
  expect(changed).not.toHaveBeenCalled();
  window.removeEventListener(AUTH_USER_CHANGE_EVENT, changed);
});
it('deduplicates a refresh for the same session and rotates credentials', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  mocks.post.mockResolvedValue({ data: { access_token: 'rotated', refresh_token: 'rotated-refresh' } });
  expect(await Promise.all([AgentAppsAuth.refreshAccessToken(), AgentAppsAuth.refreshAccessToken()])).toEqual(['rotated', 'rotated']);
  expect(mocks.post).toHaveBeenCalledTimes(1);
  expect(AgentAppsAuth.getRefreshToken()).toBe('rotated-refresh');
});
it('a replacement between refresh validation and storage write stays isolated', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  mocks.post.mockResolvedValue({ data: { access_token: 'late-A', refresh_token: 'late-refresh-A' } });
  const original = localStorage.setItem;
  const spy = vi.spyOn(localStorage, 'setItem').mockImplementation(function(this: Storage, key, value) {
    spy.mockRestore();
    // Model another process changing the active session at the commit boundary.
    AgentAppsAuth.setUserInfo(user('B'));
    original.call(this, key, value);
  });
  await AgentAppsAuth.refreshAccessToken().catch(() => {});
  expect(AgentAppsAuth.getUserInfo()).toMatchObject(user('B'));
  spy.mockRestore();
});

it('a late failed refresh cannot clear the replacement login', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  let reject!: (error: Error) => void;
  mocks.post.mockReturnValue(new Promise((_resolve, fail) => { reject = fail; }));
  const result = AgentAppsAuth.refreshAccessToken().catch(error => error);
  AgentAppsAuth.setUserInfo(user('B'));
  reject(new Error('network'));
  expect((await result).message).toBe('STALE_AUTH_SESSION');
  expect(AgentAppsAuth.getUserInfo()).toMatchObject(user('B'));
});

it('logout invalidates refresh before awaiting network cleanup and preserves a subsequent login', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  let finishRefresh!: (value: unknown) => void;
  let finishCleanup!: (value: unknown) => void;
  mocks.post.mockReturnValue(new Promise(resolve => { finishRefresh = resolve; }));
  vi.stubGlobal('fetch', vi.fn(() => new Promise(resolve => { finishCleanup = resolve; })));
  const refresh = AgentAppsAuth.refreshAccessToken().catch(error => error);
  const logout = AgentAppsAuth.logout();
  expect(AgentAppsAuth.getUserInfo()).toBeNull();
  AgentAppsAuth.setUserInfo(user('B'));
  finishRefresh({ data: { access_token: 'late-A' } });
  expect(await refresh).toBeInstanceOf(Error);
  finishCleanup({});
  await logout;
  expect(AgentAppsAuth.getUserInfo()).toMatchObject(user('B'));
  vi.unstubAllGlobals();
});
