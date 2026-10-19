import { Configuration as CoreConfiguration, TaskNotificationsApi } from '@/api/generated/core-client';
import { Configuration as GatewayConfiguration, TaskNotificationsApi as GatewayNotificationsApi, ChannelAccountsApi } from '@/api/generated/channel-gateway-client';
import type { RawAxiosRequestConfig } from 'axios';
import { axiosInstance, BASE_URL } from '@/components/request';
import { listNotificationGroups, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';
import { NOTIFICATION_PREFERENCES_CHANGED_EVENT, WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT, notificationEventScope, publishNotificationChange } from './notificationEvents';

export { NOTIFICATION_PREFERENCES_CHANGED_EVENT, WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT } from './notificationEvents';

export const providers: ChannelProvider[] = ['feishu', 'wecom', 'wechat'];
export const channels = ['desktop', ...providers] as const;
export const events = ['succeeded', 'failed', 'waiting'] as const;
export type EventName = typeof events[number];
export type ChannelName = 'desktop' | ChannelProvider;
export interface ChannelRule { enabled: boolean; account_id?: string; recipient_id?: string }
export interface NotificationConfig {
  events: Record<EventName, { enabled: boolean; content: 'summary' | 'full' }>;
  channels: Partial<Record<ChannelName, ChannelRule>>;
}
export interface NotificationUpdate { revision: number; config?: NotificationConfig; clear?: boolean }
export interface Preferences { enabled: boolean; revision: number; defaults: NotificationConfig }
export interface ScheduleNotifications {
  configured: boolean; revision: number; config: NotificationConfig | null;
  availability: Partial<Record<ChannelName, { state: string; reason: string }>>;
}
export interface Target {
  recipient_id: string; label: string; available: boolean; kind?: string;
  connection_check_status?: 'unknown' | 'checking' | 'connected' | 'activation_required' | 'unavailable';
  connection_checked_at?: string | null; connection_error_code?: string;
}
export interface ConfiguredWechatTarget { account_id: string; recipient_id: string }
export interface Reference { id: string; kind: string; name: string; enabled: boolean }
export interface Page<T> { items: T[]; next_cursor: string; total?: number }
export interface AccountDetail extends ChannelAccount {
  avatar_url?: string | null; primary_recipient: Target | null; default_recipient?: Target | null; notification_reference_count: number;
}
export interface Notice {
  notification_id: string; channel: ChannelName; account_id?: string; recipient_id?: string;
  status: string; reason?: string; created_at: string; content: string; event: string; gateway_id?: string;
}
export interface Attempt extends Pick<Notice, 'notification_id' | 'status' | 'reason' | 'created_at'> {
  source_notification_id?: string; retry_of: string; retryable: boolean; attempt_count: number; payload: Omit<Notice, 'notification_id' | 'status' | 'created_at'>;
}
export interface ExecutionNotifications { snapshot: { revision: number; config: NotificationConfig | null }; items: Notice[] }
const coreClient = new TaskNotificationsApi(new CoreConfiguration({ basePath: BASE_URL }), BASE_URL, axiosInstance);
const gatewayClient = new GatewayNotificationsApi(new GatewayConfiguration({ basePath: BASE_URL }), BASE_URL, axiosInstance);
const accountsClient = new ChannelAccountsApi(new GatewayConfiguration({ basePath: BASE_URL }), BASE_URL, axiosInstance);
// Core notification endpoints retain their business envelope; gateway returns the view directly.
const unwrap = <T,>(data: T | { data: T }): T => 'data' in (data as object) ? (data as { data: T }).data : data as T;
export const getPreferences = async (options?: { silentError?: boolean }) =>
  unwrap<Preferences>((await coreClient.apiCoreUserNotificationPreferencesGet(options as RawAxiosRequestConfig)).data as Preferences | { data: Preferences });
export const getConfiguredWechatTargets = async (options?: { silentError?: boolean }): Promise<{ items: ConfiguredWechatTarget[] }> =>
  unwrap<{ items: ConfiguredWechatTarget[] }>((await coreClient.apiCoreUserWechatNotificationTargetsGet(options as RawAxiosRequestConfig)).data as { items: ConfiguredWechatTarget[] } | { data: { items: ConfiguredWechatTarget[] } });
export const patchPreferences = async (patch: Partial<Preferences> & { revision: number; confirm_running_task_ids?: string[] }) => {
  const scope = notificationEventScope();
  const preferences = unwrap<Preferences>((await coreClient.apiCoreUserNotificationPreferencesPatch({ notificationPreferencesPatch: patch })).data as Preferences | { data: Preferences });
  // Consumers reload their current user's preferences instead of trusting an
  // asynchronous save response that may belong to an earlier login session.
  publishNotificationChange(NOTIFICATION_PREFERENCES_CHANGED_EVENT, scope);
  return preferences;
};
export const getScheduleNotifications = async (scheduleId: string) =>
  unwrap<ScheduleNotifications>((await coreClient.apiCoreSchedulesScheduleIdNotificationsGet({ scheduleId })).data as ScheduleNotifications | { data: ScheduleNotifications });
export const putScheduleNotifications = async (scheduleId: string, revision: number, config: NotificationConfig | null, silentError = false) => {
  const scope = notificationEventScope();
  const result = unwrap<ScheduleNotifications>((await coreClient.apiCoreSchedulesScheduleIdNotificationsPut(
    { scheduleId, scheduleNotificationUpdate: { revision, ...(config === null ? { clear: true } : { config }) } },
    silentError ? ({ silentError: true } as RawAxiosRequestConfig) : undefined,
  )).data as ScheduleNotifications | { data: ScheduleNotifications });
  publishNotificationChange(WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT, scope);
  return result;
};
export const getExecutionNotifications = async (taskId: string) =>
  unwrap<ExecutionNotifications>((await coreClient.apiCoreTaskCenterTasksTaskIdNotificationsGet({ taskId })).data as ExecutionNotifications | { data: ExecutionNotifications });
export const getAttempts = async (taskId: string, cursor = ''): Promise<Page<Attempt>> =>
  (await gatewayClient.listTaskNotifications({ taskId, cursor: cursor || undefined, limit: 100 })).data as Page<Attempt>;
export const retryNotice = async (noticeId: string, key: string, confirmed: boolean): Promise<Attempt> =>
  (await gatewayClient.retryTaskNotification({ notificationId: noticeId, notificationRetry: { idempotency_key: key, confirm_duplicate_risk: confirmed } })).data as Attempt;
export const getAccountDetail = async (accountId: string): Promise<AccountDetail> =>
  (await accountsClient.getChannelAccount({ accountId })).data as AccountDetail;
export const getTargets = async (accountId: string, cursor = '', recipientId = '', options?: { checkConnection?: boolean }): Promise<Page<Target>> =>
  (await accountsClient.listNotificationTargets({
    accountId, cursor, recipientId: recipientId || undefined, limit: 100,
    checkConnection: options?.checkConnection && recipientId.trim() ? true : undefined,
  }, { silentError: true } as RawAxiosRequestConfig)).data as Page<Target>;
export const getReferences = async (accountId: string, cursor = ''): Promise<Page<Reference>> =>
  (await accountsClient.listNotificationReferences({ accountId, cursor, limit: 100 })).data as Page<Reference>;
export function notificationError(error: unknown): { reason: string; running_task_ids?: string[] } {
  const data = (error as { response?: { data?: { data?: { detail?: { reason?: string; running_task_ids?: string[] } }; error?: { code?: string } } } })?.response?.data;
  return { ...data?.data?.detail, reason: data?.data?.detail?.reason || data?.error?.code || 'NOTIFICATION_UNAVAILABLE' };
}
export function emptyRule(): NotificationConfig {
  return { events: { succeeded: { enabled: true, content: 'summary' }, failed: { enabled: true, content: 'summary' }, waiting: { enabled: false, content: 'summary' } }, channels: { desktop: { enabled: false } } };
}
export function channelSwitchDefaults(config: NotificationConfig): NotificationConfig {
  const channels = { ...config.channels };
  providers.forEach(provider => { if (channels[provider]) channels[provider] = { enabled: channels[provider].enabled }; });
  return { ...config, channels };
}
export function taskRuleFromDefaults(config: NotificationConfig): NotificationConfig {
  return channelSwitchDefaults(config);
}
export function channelGateEnabled(preferences: Preferences, channel: ChannelName): boolean {
  return preferences.enabled && Boolean(preferences.defaults.channels[channel]?.enabled);
}
export function ruleError(config: NotificationConfig, defaults = false, task?: { preferences: Preferences; original?: NotificationConfig | null }): string | undefined {
  if ((defaults || Object.values(config.channels).some(c => c?.enabled)) && !events.some(e => config.events[e].enabled)) return 'NOTIFICATION_EVENT_REQUIRED';
  if (!defaults && providers.some(provider => {
    const rule = config.channels[provider];
    if (!rule?.enabled || (rule.account_id && rule.recipient_id)) return false;
    const original = task?.original?.channels[provider];
    const unchangedPaused = task && !channelGateEnabled(task.preferences, provider) && original?.enabled === rule.enabled
      && original.account_id === rule.account_id && original.recipient_id === rule.recipient_id;
    return !unchangedPaused;
  })) return 'NOTIFICATION_TARGET_REQUIRED';
}
export function availabilityError(value: ScheduleNotifications | undefined, preferences?: Preferences): { reason: string; provider: ChannelProvider; account_id: string } | undefined {
  if (!value?.config) return undefined;
  const provider = providers.find(name => value.config?.channels[name]?.enabled && (!preferences || channelGateEnabled(preferences, name)) && value.availability[name]?.state === 'unavailable');
  if (!provider) return undefined;
  return {
    reason: value.availability[provider]?.reason || 'NOTIFICATION_TARGET_UNAVAILABLE',
    provider,
    account_id: value.config.channels[provider]?.account_id || '',
  };
}

export const getGroups = listNotificationGroups;
