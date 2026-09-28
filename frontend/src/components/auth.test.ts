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

it('keeps the session and a pending rotated refresh through profile updates', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  const identity = AgentAppsAuth.getSessionIdentity();
  let done!: (value: unknown) => void;
  mocks.post.mockReturnValue(new Promise(resolve => { done = resolve; }));
  const result = AgentAppsAuth.refreshAccessToken().catch(error => error);
  AgentAppsAuth.updateUserInfo({ displayName: 'Updated name', email: 'new@example.com' });
  done({ data: { access_token: 'rotated', refresh_token: 'rotated-refresh' } });
  expect(await result).toBe('rotated');
  expect(AgentAppsAuth.getSessionIdentity()).toBe(identity);
  expect(AgentAppsAuth.getUserInfo()).toMatchObject({
    displayName: 'Updated name', email: 'new@example.com', token: 'rotated', refreshToken: 'rotated-refresh',
  });
});

it('retains rotated credentials and refresh deduplication after a profile update', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  const identity = AgentAppsAuth.getSessionIdentity();
  mocks.post.mockResolvedValueOnce({ data: { access_token: 'rotated', refresh_token: 'rotated-refresh' } });
  await AgentAppsAuth.refreshAccessToken();
  AgentAppsAuth.updateUserInfo({ displayName: 'Updated name' });
  expect(AgentAppsAuth.getSessionIdentity()).toBe(identity);
  expect(AgentAppsAuth.getRefreshToken()).toBe('rotated-refresh');
  mocks.post.mockResolvedValueOnce({ data: { access_token: 'rotated-again', refresh_token: 'refresh-again' } });
  await Promise.all([AgentAppsAuth.refreshAccessToken(), AgentAppsAuth.refreshAccessToken()]);
  expect(mocks.post).toHaveBeenLastCalledWith(mocks.api, { refresh_token: 'rotated-refresh' });
  expect(mocks.post).toHaveBeenCalledTimes(2);
  expect(AgentAppsAuth.getUserInfo()).toMatchObject({ displayName: 'Updated name', token: 'rotated-again' });
});

it.each([{ userId: 'B' }, { tenantId: 'B-tenant' }, { token: 'replacement' }])(
  'invalidates a pending refresh when identity or credentials are explicitly updated: %s', async patch => {
    AgentAppsAuth.setUserInfo(user('A'));
    let done!: (value: unknown) => void;
    mocks.post.mockReturnValue(new Promise(resolve => { done = resolve; }));
    const result = AgentAppsAuth.refreshAccessToken().catch(error => error);
    AgentAppsAuth.updateUserInfo(patch);
    done({ data: { access_token: 'late-A', refresh_token: 'late-refresh' } });
    expect((await result).message).toBe('STALE_AUTH_SESSION');
    expect(AgentAppsAuth.getUserInfo()).toMatchObject(patch);
    expect(AgentAppsAuth.getAccessToken()).not.toBe('late-A');
  },
);

it('keeps legacy sessions stable when a profile update races refresh', async () => {
  localStorage.setItem('lazymind:user', JSON.stringify(user('A')));
  const identity = AgentAppsAuth.getSessionIdentity();
  let done!: (value: unknown) => void;
  mocks.post.mockReturnValue(new Promise(resolve => { done = resolve; }));
  const result = AgentAppsAuth.refreshAccessToken().catch(error => error);
  AgentAppsAuth.updateUserInfo({ phone: '123' });
  done({ data: { access_token: 'rotated', refresh_token: 'rotated-refresh' } });
  expect(await result).toBe('rotated');
  expect(AgentAppsAuth.getSessionIdentity()).toBe(identity);
  AgentAppsAuth.updateUserInfo({ displayName: 'Legacy profile' });
  expect(AgentAppsAuth.getSessionIdentity()).toBe(identity);
  expect(AgentAppsAuth.getRefreshToken()).toBe('rotated-refresh');
});

it('uses the original legacy generation for repeated token rotations', async () => {
  localStorage.setItem('lazymind:user', JSON.stringify(user('A')));
  const identity = AgentAppsAuth.getSessionIdentity();
  mocks.post.mockResolvedValueOnce({ data: { access_token: 'first', refresh_token: 'first-refresh' } });
  await AgentAppsAuth.refreshAccessToken();
  AgentAppsAuth.updateUserInfo({ ...AgentAppsAuth.getUserInfo()!, displayName: 'Updated' });
  expect(AgentAppsAuth.getSessionIdentity()).toBe(identity);
  mocks.post.mockResolvedValueOnce({ data: { access_token: 'second', refresh_token: 'second-refresh' } });
  expect(await AgentAppsAuth.refreshAccessToken()).toBe('second');
  expect(AgentAppsAuth.getAccessToken()).toBe('second');
  expect(AgentAppsAuth.getRefreshToken()).toBe('second-refresh');
});

it('accepts a profile edit at the refresh storage boundary without losing rotated credentials', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  mocks.post.mockResolvedValue({ data: { access_token: 'rotated', refresh_token: 'rotated-refresh' } });
  const original = localStorage.setItem;
  const spy = vi.spyOn(localStorage, 'setItem').mockImplementation(function(this: Storage, key, value) {
    spy.mockRestore();
    AgentAppsAuth.updateUserInfo({ displayName: 'Concurrent profile' });
    original.call(this, key, value);
  });
  try {
    expect(await AgentAppsAuth.refreshAccessToken()).toBe('rotated');
    expect(AgentAppsAuth.getUserInfo()).toMatchObject({ displayName: 'Concurrent profile', token: 'rotated' });
  } finally {
    spy.mockRestore();
  }
});

it('does not let a late refresh overwrite renewed local credentials', async () => {
  AgentAppsAuth.setUserInfo(user('A'));
  const identity = AgentAppsAuth.getSessionIdentity();
  let done!: (value: unknown) => void;
  mocks.post.mockReturnValue(new Promise(resolve => { done = resolve; }));
  const result = AgentAppsAuth.refreshAccessToken();

  AgentAppsAuth.replaceLocalSession({ ...user('A'), token: 'local-renewed', refreshToken: 'local-refresh' });
  done({ data: { access_token: 'late-refresh', refresh_token: 'late-refresh-token' } });

  expect(await result).toBe('local-renewed');
  expect(AgentAppsAuth.getSessionIdentity()).toBe(identity);
  expect(AgentAppsAuth.getUserInfo()).toMatchObject({ token: 'local-renewed', refreshToken: 'local-refresh' });
});


it('starts a new request generation when local recovery returns another tenant', () => {
  AgentAppsAuth.setUserInfo(user('A'));
  const identity = AgentAppsAuth.getSessionIdentity();

  AgentAppsAuth.replaceLocalSession({ ...user('A'), tenantId: 'other-tenant', token: 'other-token' });

  expect(AgentAppsAuth.getSessionIdentity()).not.toBe(identity);
  expect(AgentAppsAuth.getUserInfo()).toMatchObject({ tenantId: 'other-tenant', token: 'other-token' });
});
