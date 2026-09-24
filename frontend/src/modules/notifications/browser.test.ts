import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AUTH_USER_CHANGE_EVENT, AUTH_LOGOUT_EVENT } from '@/components/auth';
import { browserNotificationsSupported, desktopNotificationsAuthorized, startBrowserNotifications } from './browser';

const state = vi.hoisted(() => ({
  user: { userId: 'owner', token: 'test-token' } as { userId: string; token: string } | null,
  desktop: false, request: vi.fn(),
}));
vi.mock('@/components/auth', () => ({ AUTH_USER_CHANGE_EVENT: 'test:auth-change', AUTH_LOGOUT_EVENT: 'test:logout', AgentAppsAuth: { getUserInfo: () => state.user } }));
vi.mock('@/components/request', () => ({ BASE_URL: 'http://localhost', axiosInstance: { request: state.request } }));
vi.mock('@/runtime/mode', () => ({ isDesktopRuntime: () => state.desktop }));
const notice = { notification_id: 'a'.repeat(64), user_id: 'owner', task_id: 'task-1', channel: 'desktop', status: 'pending', title: '每日简报', body: '今日已完成三项工作。' };
const storageKey = 'lazymind:notifications:' + JSON.stringify(['http://localhost', 'owner', '']);
let items: unknown[];
let callbacks: Array<() => void>;
let stops: Array<() => void>;
let constructors: FakeNotification[];
let autoShow: boolean;
class FakeNotification {
  static permission = 'granted';
  static requestPermission = vi.fn();
  onshow: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onclick: (() => void) | null = null;
  close = vi.fn();
  constructor(public title: string, public options: NotificationOptions) {
    constructors.push(this);
    if (autoShow) callbacks.push(() => this.onshow?.());
  }
}
async function settle() {
  for (let i = 0; i < 60; i += 1) {
    await Promise.resolve();
    callbacks.splice(0).forEach(callback => callback());
  }
}
const start = () => { stops.push(startBrowserNotifications()); };
const ackCalls = () => state.request.mock.calls.filter(([config]) => config.method === 'POST');
beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  state.user = { userId: 'owner', token: 'test-token' };
  state.desktop = false;
  callbacks = []; stops = []; constructors = []; items = [notice]; autoShow = true;
  FakeNotification.permission = 'granted';
  vi.stubGlobal('Notification', FakeNotification);
  vi.stubGlobal('isSecureContext', true);
  const held = new Set<string>();
  Object.defineProperty(navigator, 'locks', { configurable: true, value: {
    request: vi.fn(async (name: string, _options: unknown, run: (lock: object | null) => Promise<void>) => {
      if (held.has(name)) return run(null);
      held.add(name);
      try { await run({ name }); } finally { held.delete(name); }
    }),
  } });
  state.request.mockReset().mockImplementation(async config => {
    if (config.url.endsWith('/auth/me')) return { data: { user_id: state.user?.userId, status: 'active' } };
    if (config.method === 'POST') return { data: { data: { status: 'delivered' } } };
    return { data: { data: { items, device_id: 'browser', next_cursor: '' } } };
  });
});
afterEach(() => {
  stops.forEach(stop => stop());
  vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals();
});

describe('browser system notifications', () => {
  it('does not treat desktop runtime alone as notification authorization', () => {
    state.desktop = true;
    FakeNotification.permission = 'default';
    expect(desktopNotificationsAuthorized()).toBe(false);
    FakeNotification.permission = 'granted';
    expect(desktopNotificationsAuthorized()).toBe(true);
  });
  it('renders the result and acknowledges only after the browser show event', async () => {
    autoShow = false; start(); await settle();
    expect(constructors).toHaveLength(1);
    expect(constructors[0].title).toBe('LazyMind');
    expect(constructors[0].options.body).toBe(`每日简报\n${notice.body}`);
    expect(ackCalls()).toHaveLength(0);
    constructors[0].onshow?.(); await settle();
    expect(ackCalls()).toHaveLength(1);
    expect(ackCalls()[0][0].data).toEqual({ device_id: 'browser', status: 'delivered' });
  });
  it('coordinates concurrent tabs and repeated polls with one OS submission', async () => {
    start(); start(); await settle();
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(constructors).toHaveLength(1);
    expect(ackCalls()).toHaveLength(1);
  });
  it('keeps polling while the page is hidden/minimized', async () => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    items = []; start(); await settle(); items = [notice];
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(constructors).toHaveLength(1);
  });
  it('a second tab takes over after the first tab closes', async () => {
    items = []; start(); await settle(); stops[0]();
    start(); items = [notice]; await settle();
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(constructors).toHaveLength(1);
  });
  it.each(['default', 'denied'])('never requests permission or acknowledges while permission is %s', async permission => {
    FakeNotification.permission = permission; start(); await settle();
    expect(state.request).not.toHaveBeenCalled();
    expect(FakeNotification.requestPermission).not.toHaveBeenCalled();
    expect(constructors).toHaveLength(0);
  });
  it('stays disabled for Electron, insecure origins, and missing cross-tab locks', () => {
    state.desktop = true; expect(browserNotificationsSupported()).toBe(false);
    state.desktop = false; vi.stubGlobal('isSecureContext', false); expect(browserNotificationsSupported()).toBe(false);
    vi.stubGlobal('isSecureContext', true); Object.defineProperty(navigator, 'locks', { value: undefined });
    start(); expect(state.request).not.toHaveBeenCalled();
  });
  it('retries failed receipts after restart without re-displaying', async () => {
    state.request.mockImplementation(async config => {
      if (config.url.endsWith('/auth/me')) return { data: { user_id: 'owner', status: 'active' } };
      if (config.method === 'POST') throw new Error('network');
      return { data: { data: { items, device_id: 'browser', next_cursor: '' } } };
    });
    start(); await settle(); stops[0]();
    expect(JSON.parse(localStorage.getItem(storageKey)!)[notice.notification_id].status).toBe('shown');
    state.request.mockImplementation(async config => config.url.endsWith('/auth/me')
      ? { data: { user_id: 'owner', status: 'active' } }
      : { data: { data: config.method === 'POST' ? {} : { items, device_id: 'browser', next_cursor: '' } } });
    start(); await settle();
    expect(constructors).toHaveLength(1);
    expect(JSON.parse(localStorage.getItem(storageKey)!)[notice.notification_id].status).toBe('acked');
  });
  it('does not replay a crash-ambiguous submission or report it delivered', async () => {
    localStorage.setItem(storageKey, JSON.stringify({ [notice.notification_id]: { status: 'submitting' } }));
    start(); await settle();
    expect(constructors).toHaveLength(0); expect(ackCalls()).toHaveLength(0);
  });
  it('times out an unconfirmed display without a false receipt or late journal mutation', async () => {
    autoShow = false; start(); await settle();
    await vi.advanceTimersByTimeAsync(10000); await settle();
    expect(constructors[0].onshow).toBeNull();
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(constructors).toHaveLength(1); expect(ackCalls()).toHaveLength(0);
  });
  it('fails safely when storage is corrupt or unwritable', async () => {
    localStorage.setItem(storageKey, 'broken'); start(); await settle();
    expect(constructors).toHaveLength(0); stops[0](); localStorage.clear();
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('quota'); });
    start(); await settle(); expect(constructors).toHaveLength(0);
  });
  it('ignores foreign, oversized, malformed and non-pending notifications', async () => {
    items = [{ ...notice, user_id: 'other' }, { ...notice, body: '中'.repeat(201) }, { ...notice, notification_id: '../evil' }, { ...notice, status: 'skipped' }];
    start(); await settle(); expect(constructors).toHaveLength(0); expect(ackCalls()).toHaveLength(0);
  });
  it('rejects an unverified identity and resumes after transient network errors', async () => {
    state.request.mockRejectedValueOnce(new Error('offline')); start(); await settle();
    expect(constructors).toHaveLength(0);
    state.request.mockResolvedValueOnce({ data: { user_id: 'other', status: 'active' } });
    await vi.advanceTimersByTimeAsync(10000); await settle(); expect(constructors).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(5000); await settle(); expect(constructors).toHaveLength(1);
  });
  it('discards an in-flight feed after logout or switching accounts', async () => {
    let release!: (value: unknown) => void;
    state.request.mockImplementation(config => config.url.endsWith('/auth/me') ? Promise.resolve({ data: { user_id: 'owner', status: 'active' } })
      : new Promise(resolve => { release = resolve; }));
    start(); await settle();
    state.user = null; window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT));
    expect(state.request.mock.calls[1][0].signal.aborted).toBe(true);
    release({ data: { data: { items, device_id: 'browser', next_cursor: '' } } }); await settle();
    expect(constructors).toHaveLength(0); expect(ackCalls()).toHaveLength(0);
  });
  it('closes existing notifications and invalidates their clicks on cross-tab logout', async () => {
    start(); await settle(); const click = constructors[0].onclick;
    state.user = null; window.dispatchEvent(new StorageEvent('storage', { key: 'lazymind:user' }));
    expect(constructors[0].close).toHaveBeenCalled();
    const count = state.request.mock.calls.length; click?.(); await settle();
    expect(state.request).toHaveBeenCalledTimes(count);
  });
  it('stops immediately when logout begins before credentials are cleared', async () => {
    start(); await settle(); window.dispatchEvent(new Event(AUTH_LOGOUT_EVENT));
    expect(constructors[0].close).toHaveBeenCalled();
    const count = state.request.mock.calls.length;
    await vi.advanceTimersByTimeAsync(20000); await settle();
    expect(state.request).toHaveBeenCalledTimes(count);
  });
  it('stops on pagehide and resumes after a back/forward cache restore', async () => {
    items = []; start(); await settle(); window.dispatchEvent(new Event('pagehide'));
    items = [notice]; await vi.advanceTimersByTimeAsync(20000); await settle(); expect(constructors).toHaveLength(0);
    window.dispatchEvent(new Event('pageshow')); await settle(); expect(constructors).toHaveLength(1);
  });
  it('bounds repeated pagination cursors', async () => {
    state.request.mockImplementation(async config => config.url.endsWith('/auth/me')
      ? { data: { user_id: 'owner', status: 'active' } }
      : { data: { data: { items: [], device_id: 'browser', next_cursor: 'same' } } });
    start(); await settle(); expect(state.request).toHaveBeenCalledTimes(3);
  });
});

it.each([1999, 2000, 2001])('eventually scans past %i uncertain submissions without replaying them', async count => {
  const prefix = Array.from({ length: count }, (_, index) => ({ ...notice, notification_id: index.toString(16).padStart(64, '0') }));
  const all = [...prefix, notice];
  localStorage.setItem(storageKey, JSON.stringify(Object.fromEntries(prefix.map(item => [item.notification_id, { status: 'submitting' }]))));
  state.request.mockImplementation(async config => {
    if (config.url.endsWith('/auth/me')) return { data: { user_id: 'owner', status: 'active' } };
    if (config.method === 'POST') return { data: {} };
    const offset = Number(new URL(config.url).searchParams.get('cursor') || 0);
    return { data: { items: all.slice(offset, offset + 100), device_id: 'browser', next_cursor: offset + 100 < all.length ? String(offset + 100) : '' } };
  });
  start();
  for (let i = 0; i < 4; i++) { await settle(); await vi.advanceTimersByTimeAsync(5000); }
  expect(constructors).toHaveLength(1);
  expect(constructors[0].options.tag).toBe(notice.notification_id);
});

it('restarts a bounded scan after the server rejects an obsolete cursor', async () => {
  let invalid = false;
  const cursors: string[] = [];
  state.request.mockImplementation(async config => {
    if (config.url.endsWith('/auth/me')) return { data: { user_id: state.user?.userId, status: 'active' } };
    const cursor = new URL(config.url).searchParams.get('cursor') || '';
    cursors.push(cursor);
    if (invalid && cursor) throw { response: { status: 422 } };
    return { data: { items: [], device_id: 'browser', next_cursor: invalid ? '' : String(Number(cursor) + 1) } };
  });
  start(); await settle(); await settle();
  expect(cursors).toHaveLength(20);
  invalid = true;
  await vi.advanceTimersByTimeAsync(5000);
  await vi.advanceTimersByTimeAsync(10000);
  expect(cursors[cursors.length - 1]).toBe('');
});

it('does not transfer a continuation cursor to a different account', async () => {
  const firstByUser: Record<string, string> = {};
  state.request.mockImplementation(async config => {
    if (config.url.endsWith('/auth/me')) return { data: { user_id: state.user?.userId, status: 'active' } };
    const cursor = new URL(config.url).searchParams.get('cursor') || '';
    firstByUser[state.user!.userId] ??= cursor;
    return { data: { items: [], device_id: 'browser', next_cursor: String(Number(cursor) + 1) } };
  });
  start(); await settle(); await settle();
  state.user = { userId: 'B', token: 'B' };
  window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT));
  await settle(); await settle();
  expect(firstByUser).toEqual({ owner: '', B: '' });
});
