import { Configuration as CoreConfiguration, TaskNotificationsApi } from '@/api/generated/core-client';
import { Configuration as GatewayConfiguration, TaskNotificationsApi as GatewayNotificationsApi, ChannelAccountsApi } from '@/api/generated/channel-gateway-client';
import { axiosInstance, BASE_URL } from '@/components/request';
import { listNotificationGroups, updateDefaultRecipient, type ChannelAccount, type ChannelProvider } from '@/modules/channelGateway/api';

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
export interface Target { recipient_id: string; label: string; available: boolean; kind?: string }
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
export const getPreferences = async () =>
  unwrap<Preferences>((await coreClient.apiCoreUserNotificationPreferencesGet()).data as Preferences | { data: Preferences });
export const patchPreferences = async (patch: Partial<Preferences> & { revision: number; confirm_running_task_ids?: string[] }) =>
  unwrap<Preferences>((await coreClient.apiCoreUserNotificationPreferencesPatch({ notificationPreferencesPatch: patch })).data as Preferences | { data: Preferences });
export const getScheduleNotifications = async (scheduleId: string) =>
  unwrap<ScheduleNotifications>((await coreClient.apiCoreSchedulesScheduleIdNotificationsGet({ scheduleId })).data as ScheduleNotifications | { data: ScheduleNotifications });
export const putScheduleNotifications = async (scheduleId: string, revision: number, config: NotificationConfig | null) =>
  unwrap<ScheduleNotifications>((await coreClient.apiCoreSchedulesScheduleIdNotificationsPut({ scheduleId, scheduleNotificationUpdate: { revision, ...(config === null ? { clear: true } : { config }) } })).data as ScheduleNotifications | { data: ScheduleNotifications });
export const getExecutionNotifications = async (taskId: string) =>
  unwrap<ExecutionNotifications>((await coreClient.apiCoreTaskCenterTasksTaskIdNotificationsGet({ taskId })).data as ExecutionNotifications | { data: ExecutionNotifications });
export const getAttempts = async (taskId: string, cursor = ''): Promise<Page<Attempt>> =>
  (await gatewayClient.listTaskNotifications({ taskId, cursor: cursor || undefined, limit: 100 })).data as Page<Attempt>;
export const retryNotice = async (noticeId: string, key: string, confirmed: boolean): Promise<Attempt> =>
  (await gatewayClient.retryTaskNotification({ notificationId: noticeId, notificationRetry: { idempotency_key: key, confirm_duplicate_risk: confirmed } })).data as Attempt;
export const getAccountDetail = async (accountId: string): Promise<AccountDetail> =>
  (await accountsClient.getChannelAccount({ accountId })).data as AccountDetail;
export const getTargets = async (accountId: string, cursor = '', recipientId = ''): Promise<Page<Target>> =>
  (await accountsClient.listNotificationTargets({ accountId, cursor, recipientId: recipientId || undefined, limit: 100 })).data as Page<Target>;
export const getReferences = async (accountId: string, cursor = ''): Promise<Page<Reference>> =>
  (await accountsClient.listNotificationReferences({ accountId, cursor, limit: 100 })).data as Page<Reference>;
export function notificationError(error: unknown): { reason: string; running_task_ids?: string[] } {
  const data = (error as { response?: { data?: { data?: { detail?: { reason?: string; running_task_ids?: string[] } }; error?: { code?: string } } } })?.response?.data;
  return { ...data?.data?.detail, reason: data?.data?.detail?.reason || data?.error?.code || 'NOTIFICATION_UNAVAILABLE' };
}
export function emptyRule(): NotificationConfig {
  return { events: { succeeded: { enabled: true, content: 'summary' }, failed: { enabled: true, content: 'summary' }, waiting: { enabled: false, content: 'summary' } }, channels: { desktop: { enabled: false } } };
}
export function ruleError(config: NotificationConfig, defaults = false): string | undefined {
  if ((defaults || Object.values(config.channels).some(c => c?.enabled)) && !events.some(e => config.events[e].enabled)) return 'NOTIFICATION_EVENT_REQUIRED';
  if (!defaults && providers.some(p => config.channels[p]?.enabled && !config.channels[p]?.account_id)) return 'NOTIFICATION_TARGET_REQUIRED';
}
export function availabilityError(value: ScheduleNotifications | undefined): { reason: string; provider: ChannelProvider; account_id: string } | undefined {
  if (!value?.config) return undefined;
  const provider = providers.find(name => value.config?.channels[name]?.enabled && value.availability[name]?.state === 'unavailable');
  if (!provider) return undefined;
  return {
    reason: value.availability[provider]?.reason || 'NOTIFICATION_TARGET_UNAVAILABLE',
    provider,
    account_id: value.config.channels[provider]?.account_id || '',
  };
}

export const getGroups = listNotificationGroups;
export const setDefaultRecipient = updateDefaultRecipient;
