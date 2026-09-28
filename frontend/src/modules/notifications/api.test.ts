import { beforeEach, describe, expect, it, vi } from 'vitest';
import { availabilityError, emptyRule, getPreferences, getScheduleNotifications, notificationError, patchPreferences, putScheduleNotifications, retryNotice, ruleError } from './api';
import { createConnectionSession } from '@/modules/channelGateway/api';
const http = vi.hoisted(() => ({ request: vi.fn(), defaults: {}, post: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => vi.clearAllMocks());
describe('notification API contracts', () => {
  it('unwraps preferences and preserves revision and exact run confirmation', async () => {
    const prefs = { revision: 8, enabled: false, defaults: emptyRule() };
    http.request.mockResolvedValue({ data: { code: 0, data: prefs } });
    expect(await getPreferences()).toEqual(prefs);
    http.request.mockResolvedValue({ data: { data: prefs } });
    const patch = { revision: 7, enabled: false, confirm_running_task_ids: ['run-a'] };
    expect(await patchPreferences(patch)).toEqual(prefs);
    expect(http.request).toHaveBeenLastCalledWith(expect.objectContaining({ method: 'PATCH', url: '/api/core/user/notification-preferences', data: JSON.stringify(patch) }));
  });
  it('preserves unconfigured legacy schedules and URL-encodes opaque IDs', async () => {
    const value = { configured: false, config: null, revision: 0, availability: {} };
    http.request.mockResolvedValue({ data: { data: value } });
    expect(await getScheduleNotifications('a/b')).toEqual(value);
    expect(http.request).toHaveBeenCalledWith(expect.objectContaining({ method: 'GET', url: '/api/core/schedules/a%2Fb/notifications' }));
    http.request.mockResolvedValue({ data: { data: value } });
    await putScheduleNotifications('a/b', 0, emptyRule());
    expect(http.request).toHaveBeenLastCalledWith(expect.objectContaining({ method: 'PUT', url: '/api/core/schedules/a%2Fb/notifications', data: JSON.stringify({ revision: 0, config: emptyRule() }) }));
  });
  it('sends public WeCom credentials and original account ID on reconnect', async () => {
    http.post.mockResolvedValue({ data: { status: 'connected' } });
    await createConnectionSession('wecom', { accountId: 'original', credentials: { bot_id: 'test-bot', secret: 'test-secret' }, idempotencyKey: 'operation' });
    expect(http.post).toHaveBeenCalledWith('/api/channel-gateway/v1/connection-sessions', { provider: 'wecom', account_id: 'original', credentials: { bot_id: 'test-bot', secret: 'test-secret' } }, { headers: { 'Idempotency-Key': 'operation' } });
  });
  it('sends the stable retry key and risk confirmation without running a task', async () => {
    http.request.mockResolvedValue({ data: { notification_id: 'retry' } });
    await retryNotice('notice/a', 'operation', true);
    expect(http.request).toHaveBeenLastCalledWith(expect.objectContaining({ method: 'POST', url: '/api/channel-gateway/v1/task-notifications/notice%2Fa:retry', data: JSON.stringify({ idempotency_key: 'operation', confirm_duplicate_risk: true }) }));
  });
  it('requires events and an external account but allows its default recipient', () => {
    const config = emptyRule(); Object.values(config.events).forEach(e => { e.enabled = false; });
    expect(ruleError(config)).toBeUndefined(); expect(ruleError(config, true)).toBe('NOTIFICATION_EVENT_REQUIRED');
    config.events.succeeded.enabled = true; config.channels.wecom = { enabled: true };
    expect(ruleError(config)).toBe('NOTIFICATION_TARGET_REQUIRED');
    config.channels.wecom = { enabled: true, account_id: 'a' };
    expect(ruleError(config)).toBeUndefined();
  });
  it('extracts safe reasons without displaying raw errors', () => {
    expect(notificationError({ response: { data: { data: { detail: { reason: 'NOTIFICATION_CONFIRMATION_REQUIRED', running_task_ids: ['r'] } } } } })).toEqual({ reason: 'NOTIFICATION_CONFIRMATION_REQUIRED', running_task_ids: ['r'] });
    expect(notificationError(new Error('secret SQL')).reason).toBe('NOTIFICATION_UNAVAILABLE');
    expect(notificationError({ response: { data: { error: { code: 'NOTIFICATION_STATE_CHANGED' } } } }).reason).toBe('NOTIFICATION_STATE_CHANGED');
  });
  it('guides users to restore the unavailable provider context', () => {
    const config = emptyRule();
    config.channels.feishu = { enabled: true, account_id: 'f', recipient_id: 'group' };
    config.channels.wecom = { enabled: true, account_id: 'c', recipient_id: 'room' };
    config.channels.wechat = { enabled: true, account_id: 'w', recipient_id: 'user' };
    expect(availabilityError({ configured: true, revision: 1, config, availability: {
      feishu: { state: 'available', reason: '' },
      wecom: { state: 'available', reason: '' },
      wechat: { state: 'unavailable', reason: 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED' },
    } })).toEqual({ reason: 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED', provider: 'wechat', account_id: 'w' });
  });
});
