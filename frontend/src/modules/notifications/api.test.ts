import { beforeEach, describe, expect, it, vi } from 'vitest';
import { availabilityError, channelSwitchDefaults, emptyRule, getConfiguredWechatTargets, getPreferences, getScheduleNotifications, getTargets, NOTIFICATION_PREFERENCES_CHANGED_EVENT, WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT, notificationError, patchPreferences, putScheduleNotifications, retryNotice, ruleError, taskRuleFromDefaults } from './api';
import { createConnectionSession } from '@/modules/channelGateway/api';
const http = vi.hoisted(() => ({ request: vi.fn(), defaults: {}, post: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
beforeEach(() => vi.clearAllMocks());
describe('notification API contracts', () => {
  it('reads confirmed WeChat recipients without triggering provider checks', async () => {
    const targets = { items: [{ account_id: 'account', recipient_id: 'user' }] };
    http.request.mockResolvedValue({ data: { code: 0, data: targets } });
    expect(await getConfiguredWechatTargets({ silentError: true })).toEqual(targets);
    expect(http.request).toHaveBeenCalledTimes(1);
    expect(http.request).toHaveBeenLastCalledWith(expect.objectContaining({
      method: 'GET', url: '/api/core/user/wechat-notification-targets', silentError: true,
    }));
  });
  it('only opts into connection checks for an explicitly confirmed recipient', async () => {
    const targets = { items: [{ recipient_id: 'user/a', label: 'User', available: false,
      connection_check_status: 'activation_required', connection_checked_at: null,
      connection_error_code: 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED' }], next_cursor: '' };
    http.request.mockResolvedValue({ data: targets });
    expect(await getTargets('account/a', '', 'user/a', { checkConnection: true })).toEqual(targets);
    let request = http.request.mock.lastCall![0];
    let url = new URL(request.url, 'http://localhost');
    expect(url.pathname).toBe('/api/channel-gateway/v1/channel-accounts/account%2Fa/notification-targets');
    expect(url.searchParams.get('recipient_id')).toBe('user/a');
    expect(url.searchParams.get('check_connection')).toBe('true');
    expect(request.silentError).toBe(true);
    expect(request).not.toHaveProperty('params');
    await getTargets('account/a', '', 'user/a');
    url = new URL(http.request.mock.lastCall![0].url, 'http://localhost');
    expect(url.searchParams.has('check_connection')).toBe(false);
    await getTargets('account/a', '', '', { checkConnection: true });
    request = http.request.mock.lastCall![0];
    url = new URL(request.url, 'http://localhost');
    expect(url.searchParams.has('check_connection')).toBe(false);
    expect(url.searchParams.has('recipient_id')).toBe(false);
  });
  it('refreshes confirmed recipient monitoring only after a successful schedule save or clear', async () => {
    const listener = vi.fn();
    window.addEventListener(WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT, listener);
    try {
      const config = emptyRule();
      config.channels.wechat = { enabled: true, account_id: 'account', recipient_id: 'user' };
      const saved = { configured: true, config, revision: 2, availability: {} };
      http.request.mockResolvedValue({ data: { code: 0, data: saved } });
      expect(await putScheduleNotifications('schedule', 1, config)).toEqual(saved);
      expect(listener).toHaveBeenCalledTimes(1);
      http.request.mockRejectedValueOnce(new Error('save failed'));
      await expect(putScheduleNotifications('schedule', 2, null)).rejects.toThrow('save failed');
      expect(listener).toHaveBeenCalledTimes(1);
      http.request.mockResolvedValue({ data: { data: { configured: false, config: null, revision: 3, availability: {} } } });
      await putScheduleNotifications('schedule', 2, null);
      expect(listener).toHaveBeenCalledTimes(2);
      expect(http.request).toHaveBeenLastCalledWith(expect.objectContaining({ data: JSON.stringify({ revision: 2, clear: true }) }));
    } finally { window.removeEventListener(WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT, listener); }
  });
  it('supports silent background checks and refreshes monitoring only after a successful preference save', async () => {
    const listener = vi.fn();
    window.addEventListener(NOTIFICATION_PREFERENCES_CHANGED_EVENT, listener);
    try {
      const prefs = { revision: 8, enabled: true, defaults: emptyRule() };
      http.request.mockResolvedValue({ data: { data: prefs } });
      await getPreferences({ silentError: true });
      expect(http.request).toHaveBeenLastCalledWith(expect.objectContaining({ method: 'GET', silentError: true }));
      await patchPreferences({ revision: 7, enabled: true });
      expect(listener).toHaveBeenCalledTimes(1);
      http.request.mockRejectedValueOnce(new Error('save failed'));
      await expect(patchPreferences({ revision: 8, enabled: false })).rejects.toThrow('save failed');
      expect(listener).toHaveBeenCalledTimes(1);
    } finally { window.removeEventListener(NOTIFICATION_PREFERENCES_CHANGED_EVENT, listener); }
  });
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
    await putScheduleNotifications('a/b', 0, emptyRule(), true);
    expect(http.request).toHaveBeenLastCalledWith(expect.objectContaining({ method: 'PUT', silentError: true }));
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
  it('requires events and an explicit task recipient', () => {
    const config = emptyRule(); Object.values(config.events).forEach(e => { e.enabled = false; });
    expect(ruleError(config)).toBeUndefined(); expect(ruleError(config, true)).toBe('NOTIFICATION_EVENT_REQUIRED');
    config.events.succeeded.enabled = true; config.channels.wecom = { enabled: true };
    expect(ruleError(config)).toBe('NOTIFICATION_TARGET_REQUIRED');
    config.channels.wecom = { enabled: true, account_id: 'a' };
    expect(ruleError(config)).toBe('NOTIFICATION_TARGET_REQUIRED');
    config.channels.wecom = { enabled: true, account_id: 'a', recipient_id: 'group' };
    expect(ruleError(config)).toBeUndefined();
  });
  it('keeps global channel switches separate from task recipients', () => {
    const config = emptyRule();
    config.channels.wecom = { enabled: true, account_id: 'old-account', recipient_id: 'old-group' };
    expect(channelSwitchDefaults(config).channels.wecom).toEqual({ enabled: true });
    expect(taskRuleFromDefaults(config).channels.wecom).toEqual({ enabled: true });
    expect(config.channels.wecom).toEqual({ enabled: true, account_id: 'old-account', recipient_id: 'old-group' });
  });
  it('only exempts unchanged paused channels from target validation', () => {
    const original = emptyRule();
    original.channels.feishu = { enabled: true };
    const preferences = { revision: 1, enabled: true, defaults: emptyRule() };
    const changed = { ...original, channels: { ...original.channels, feishu: { enabled: true, account_id: 'new-account' } } };
    expect(ruleError(original, false, { preferences, original })).toBeUndefined();
    expect(ruleError(changed, false, { preferences, original })).toBe('NOTIFICATION_TARGET_REQUIRED');
    preferences.defaults.channels.feishu = { enabled: true };
    expect(ruleError(original, false, { preferences, original })).toBe('NOTIFICATION_TARGET_REQUIRED');
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
