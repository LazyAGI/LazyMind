import { AgentAppsAuth } from '@/components/auth';

export const NOTIFICATION_PREFERENCES_CHANGED_EVENT = 'lazymind:notification-preferences-changed';
export const WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT = 'lazymind:wechat-notification-config-changed';
export const WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT = 'lazymind:wechat-notification-check-requested';

const storageKey = 'lazymind:notification-change';
type ChangeEvent = typeof NOTIFICATION_PREFERENCES_CHANGED_EVENT | typeof WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT;
export function notificationEventScope() {
  const user = AgentAppsAuth.getUserInfo();
  return user ? JSON.stringify([user.userId || user.username,
    user.tenantId || user.tenant_id || user.tenantKey || user.tenant_key || '']) : null;
}

export function publishNotificationChange(event: ChangeEvent, scope = notificationEventScope()) {
  if (scope === notificationEventScope()) window.dispatchEvent(new Event(event));
  if (!scope) return;
  // Other windows receive only invalidation metadata, never saved data or credentials.
  try { localStorage.setItem(storageKey, JSON.stringify({ event, scope, nonce: Math.random() })); }
  catch { /* Focus refresh still works when browser storage is unavailable. */ }
}

export function subscribeNotificationChange(event: ChangeEvent, listener: () => void, refreshOnFocus = false) {
  const storage = (change: StorageEvent) => {
    if (change.key !== storageKey || !change.newValue) return;
    try {
      const value = JSON.parse(change.newValue);
      if (value.event === event && value.scope && value.scope === notificationEventScope()) listener();
    } catch { /* Ignore unrelated or malformed storage values. */ }
  };
  window.addEventListener(event, listener);
  window.addEventListener('storage', storage);
  if (refreshOnFocus) window.addEventListener('focus', listener);
  return () => {
    window.removeEventListener(event, listener);
    window.removeEventListener('storage', storage);
    if (refreshOnFocus) window.removeEventListener('focus', listener);
  };
}
