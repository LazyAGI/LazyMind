import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT, AUTH_LOGOUT_EVENT } from '@/components/auth';
import { axiosInstance, BASE_URL } from '@/components/request';
import { isDesktopRuntime } from '@/runtime/mode';

const FEED = '/api/core/task-center/desktop-notifications';
const MAX_ENTRIES = 5000;
const id = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 256;
const noticeID = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
type Entry = { status: 'submitting' | 'shown' | 'acked' };
type Journal = Record<string, Entry>;
interface Notice { notification_id: string; user_id: string; task_id: string; channel: string; status: string; title: string; body: string }
interface Page { device_id: string; items: Notice[]; next_cursor: string }
const unwrap = <T,>(value: T | { data: T }): T => value && typeof value === 'object' && 'data' in value ? value.data : value as T;

export function browserNotificationsSupported(): boolean {
  return !isDesktopRuntime() && window.isSecureContext && 'Notification' in window && Boolean(navigator.locks);
}

export function desktopNotificationsAuthorized(): boolean {
  return 'Notification' in window && Notification.permission === 'granted';
}

// Mounted for the application lifetime, independently of the current route or visibility.
export function startBrowserNotifications(): () => void {
  if (!browserNotificationsSupported()) return () => {};
  let stopped = false;
  let suspended = false;
  let loggingOut = false;
  let generation = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let inFlight = false;
  let delay = 5000;
  let identity = '';
  let scanCursor = '';
  const requests = new Set<AbortController>();
  const native = new Set<Notification>();
  const user = () => {
    const value = AgentAppsAuth.getUserInfo();
    return value?.token && id(value.userId) ? value : null;
  };
  const scope = () => {
    const value = user();
    return value ? JSON.stringify([BASE_URL, value.userId, value.tenantId || value.tenant_id || value.tenantKey || value.tenant_key || '']) : '';
  };
  const close = () => {
    for (const notification of native) {
      notification.onclick = notification.onshow = notification.onerror = notification.onclose = null;
      notification.close();
    }
    native.clear();
  };
  const invalidate = () => {
    generation += 1;
    scanCursor = '';
    for (const request of requests) request.abort();
    close();
  };

  async function poll() {
    if (stopped || suspended || loggingOut || inFlight) return;
    clearTimeout(timer);
    inFlight = true;
    const currentScope = scope();
    if (currentScope !== identity) { invalidate(); identity = currentScope; }
    const version = generation;
    const alive = () => !stopped && !suspended && !loggingOut && version === generation && currentScope !== '' && scope() === currentScope;
    const allowed = () => alive() && Notification.permission === 'granted';
    async function request<T>(route: string, body?: object): Promise<T> {
      if (!alive()) throw new Error('STALE_SESSION');
      const controller = new AbortController();
      requests.add(controller);
      try {
        const options = {
          url: BASE_URL + route, method: body ? 'POST' : 'GET', data: body,
          signal: controller.signal, timeout: 10000, silentError: true,
        };
        const response = await axiosInstance.request(options);
        if (!alive()) throw new Error('STALE_SESSION');
        return unwrap<T>(response.data);
      } finally { requests.delete(controller); }
    }
    try {
      if (!allowed()) return;
      // A short-lived exclusive lock is released on tab destruction. Background tabs
      // remain eligible; another tab can take over on its next poll without a lease race.
      await navigator.locks.request(`lazymind:notifications:${currentScope}`, { ifAvailable: true }, async lock => {
        if (!lock || !allowed()) return;
        const me = await request<{ user_id: string; status: string }>('/api/authservice/auth/me');
        if (!allowed() || me.user_id !== user()?.userId || me.status !== 'active') return;
        const key = `lazymind:notifications:${currentScope}`;
        const raw = localStorage.getItem(key);
        if (raw && raw.length > 1024 * 1024) throw new Error('NOTIFICATION_STORAGE_UNAVAILABLE');
        const journal: Journal = raw ? JSON.parse(raw) : {};
        if (!journal || Array.isArray(journal) || typeof journal !== 'object' || Object.keys(journal).length > MAX_ENTRIES
          || Object.entries(journal).some(([key, value]) => !noticeID(key) || !value || !['submitting', 'shown', 'acked'].includes(value.status))) {
          throw new Error('NOTIFICATION_STORAGE_UNAVAILABLE');
        }
        const persist = () => localStorage.setItem(key, JSON.stringify(journal));
        // Fail closed before requesting notifications when browser storage is disabled.
        persist();
        const acknowledge = async (notificationID: string) => {
          if (!allowed() || journal[notificationID]?.status !== 'shown') return;
          await request(`${FEED}/${notificationID}:ack`, { device_id: 'browser', status: 'delivered' });
          journal[notificationID].status = 'acked';
          persist();
        };
        // Recover successful displays whose receipts failed, without displaying again.
        for (const notificationID of Object.keys(journal).filter(key => journal[key].status === 'shown').slice(0, 100)) {
          if (journal[notificationID].status === 'shown') {
            try { await acknowledge(notificationID); } catch { /* Retry on the next poll. */ }
          }
        }
        let cursor = scanCursor;
        const seen = new Set<string>();
        for (let page = 0; page < 20 && allowed(); page += 1) {
          const data = await request<Page>(`${FEED}?device_id=browser&limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
          if (!allowed()) return;
          if (data.device_id !== 'browser' || !Array.isArray(data.items) || data.items.length > 100
            || typeof data.next_cursor !== 'string' || data.next_cursor.length > 512) throw new Error('INVALID_NOTIFICATION_RESPONSE');
          for (const item of data.items) {
            if (!allowed()) return;
            if (item.user_id !== me.user_id || item.channel !== 'desktop' || item.status !== 'pending'
              || !noticeID(item.notification_id) || !id(item.task_id) || !id(item.title)
              || typeof item.body !== 'string' || [...item.body].length > 200 || journal[item.notification_id]) continue;
            if (Object.keys(journal).length >= MAX_ENTRIES) {
              const completed = Object.keys(journal).find(key => journal[key].status === 'acked');
              if (!completed) throw new Error('NOTIFICATION_STORAGE_FULL');
              delete journal[completed];
            }
            // Browser submission and storage are not transactional. Unknown submissions
            // are retained and never automatically displayed twice after a crash.
            journal[item.notification_id] = { status: 'submitting' };
            persist();
            await new Promise<void>(resolve => {
              let notification: Notification | undefined;
              const finish = () => {
                clearTimeout(timeout);
                // No callback may mutate this journal after the lock has been released.
                if (notification) notification.onshow = notification.onerror = null;
                resolve();
              };
              const timeout = setTimeout(finish, 10000);
              try {
                notification = new Notification('LazyMind', {
                  body: `${item.title}\n${item.body}`, tag: item.notification_id,
                  icon: `${window.BASENAME || ''}/Lazy-c.png`,
                });
                if (native.size >= MAX_ENTRIES) {
                  const oldest = native.values().next().value;
                  oldest?.close();
                  if (oldest) native.delete(oldest);
                }
                native.add(notification);
                const shown = notification;
                notification.onshow = () => {
                  if (allowed()) {
                    try {
                      journal[item.notification_id].status = 'shown';
                      persist();
                    } catch {
                      journal[item.notification_id].status = 'submitting';
                    }
                  } else shown.close();
                  finish();
                };
                notification.onerror = finish;
                notification.onclose = () => { native.delete(shown); finish(); };
                notification.onclick = () => {
                  if (!allowed()) return;
                  window.focus();
                  void request<{ conversation_id?: string }>(`/api/core/task-center/tasks/${encodeURIComponent(item.task_id)}`).then(task => {
                    if (!allowed()) return;
                    const route = id(task.conversation_id) ? `/agent/chat/home/${encodeURIComponent(task.conversation_id)}` : '/task-center?tab=tasks';
                    // Build a same-origin route; never trust a URL supplied in the payload.
                    window.location.assign(`${window.BASENAME || ''}${route}`);
                  }).catch(() => {});
                };
              } catch { finish(); }
            });
            if (!allowed()) return;
            try { await acknowledge(item.notification_id); } catch { /* Persisted show; retry receipt only. */ }
          }
          cursor = data.next_cursor;
          scanCursor = cursor;
          if (!cursor || seen.has(cursor)) { scanCursor = ''; break; }
          seen.add(cursor);
        }
      });
      delay = 5000;
    } catch (error) {
      // Cursor validity may change after a service restart. Only reset on an
      // explicit bad-cursor response; transient network failures retain progress.
      if ((error as { response?: { status?: number } })?.response?.status === 422) scanCursor = '';
      delay = Math.min(delay * 2, 60000);
    }
    finally {
      inFlight = false;
      if (!stopped && !suspended && !loggingOut) timer = setTimeout(() => { void poll(); }, delay);
    }
  }
  function changed() {
    const next = scope();
    if (next !== identity) { invalidate(); identity = next; if (next) loggingOut = false; }
    void poll();
  }
  function storageChanged(event: StorageEvent) {
    if (event.key === null || event.key === 'lazymind:user') changed();
  }
  const logout = () => { loggingOut = true; invalidate(); clearTimeout(timer); };
  const pageHidden = () => { suspended = true; invalidate(); clearTimeout(timer); };
  const pageShown = () => { suspended = false; changed(); };
  window.addEventListener(AUTH_USER_CHANGE_EVENT, changed);
  window.addEventListener(AUTH_LOGOUT_EVENT, logout);
  window.addEventListener('storage', storageChanged);
  window.addEventListener('focus', changed);
  window.addEventListener('online', changed);
  window.addEventListener('pagehide', pageHidden);
  window.addEventListener('pageshow', pageShown);
  void poll();
  return () => {
    stopped = true;
    invalidate();
    clearTimeout(timer);
    window.removeEventListener(AUTH_USER_CHANGE_EVENT, changed);
    window.removeEventListener(AUTH_LOGOUT_EVENT, logout);
    window.removeEventListener('storage', storageChanged);
    window.removeEventListener('focus', changed);
    window.removeEventListener('online', changed);
    window.removeEventListener('pagehide', pageHidden);
    window.removeEventListener('pageshow', pageShown);
  };
}
