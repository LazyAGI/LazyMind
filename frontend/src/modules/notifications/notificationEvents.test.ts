import { afterEach, expect, it, vi } from 'vitest';
import { AgentAppsAuth } from '@/components/auth';
import { NOTIFICATION_PREFERENCES_CHANGED_EVENT as event, notificationEventScope, publishNotificationChange, subscribeNotificationChange } from './notificationEvents';

afterEach(() => { vi.restoreAllMocks(); localStorage.removeItem('lazymind:notification-change'); });

it('publishes credential-free invalidations and receives only the current user and tenant', () => {
  vi.spyOn(AgentAppsAuth, 'getUserInfo').mockReturnValue({ userId: 'a', username: 'A', tenantId: 'tenant-a', token: 'secret-token' });
  const listener = vi.fn(), stop = subscribeNotificationChange(event, listener);
  try {
    publishNotificationChange(event);
    expect(listener).toHaveBeenCalledTimes(1);
    const payload = localStorage.getItem('lazymind:notification-change')!;
    expect(payload).not.toContain('secret-token');
    window.dispatchEvent(new StorageEvent('storage', { key: 'lazymind:notification-change', newValue: payload }));
    expect(listener).toHaveBeenCalledTimes(2);
    for (const scope of [JSON.stringify(['b', 'tenant-a']), JSON.stringify(['a', 'tenant-b'])]) {
      window.dispatchEvent(new StorageEvent('storage', { key: 'lazymind:notification-change', newValue: JSON.stringify({ event, scope }) }));
    }
    expect(listener).toHaveBeenCalledTimes(2);
    const previous = notificationEventScope();
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({ userId: 'b', username: 'B', token: 'other-secret' });
    publishNotificationChange(event, previous);
    expect(listener).toHaveBeenCalledTimes(2);
  } finally { stop(); }
  window.dispatchEvent(new Event(event));
  expect(listener).toHaveBeenCalledTimes(2);
});
