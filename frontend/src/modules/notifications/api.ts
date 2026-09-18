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
  retry_of: string; retryable: boolean; attempt_count: number; payload: Notice;
}
export interface ExecutionNotifications { snapshot: { revision: number; config: NotificationConfig | null }; items: Notice[] }
const core = `${BASE_URL}/api/core`;
const gateway = `${BASE_URL}/api/channel-gateway/v1`;
const id = encodeURIComponent;
// Core notification endpoints retain their business envelope; gateway returns the view directly.
const unwrap = <T,>(data: T | { data: T }): T => 'data' in (data as object) ? (data as { data: T }).data : data as T;
export const getPreferences = async () => unwrap<Preferences>((await axiosInstance.get(`${core}/user/notification-preferences`)).data);
export const patchPreferences = async (patch: Partial<Preferences> & { revision: number; confirm_running_task_ids?: string[] }) =>
  unwrap<Preferences>((await axiosInstance.patch(`${core}/user/notification-preferences`, patch)).data);
export const getScheduleNotifications = async (scheduleId: string) =>
  unwrap<ScheduleNotifications>((await axiosInstance.get(`${core}/schedules/${id(scheduleId)}/notifications`)).data);
export const putScheduleNotifications = async (scheduleId: string, revision: number, config: NotificationConfig | null) =>
  unwrap<ScheduleNotifications>((await axiosInstance.put(`${core}/schedules/${id(scheduleId)}/notifications`, { revision, ...(config === null ? { clear: true } : { config }) })).data);
export const getExecutionNotifications = async (taskId: string) =>
  unwrap<ExecutionNotifications>((await axiosInstance.get(`${core}/task-center/tasks/${id(taskId)}/notifications`)).data);
export const getAttempts = async (taskId: string, cursor = ''): Promise<Page<Attempt>> =>
  (await axiosInstance.get(`${gateway}/task-notifications`, { params: { task_id: taskId, cursor: cursor || undefined, limit: 100 } })).data;
export const retryNotice = async (noticeId: string, key: string, confirmed: boolean): Promise<Attempt> =>
  (await axiosInstance.post(`${gateway}/task-notifications/${id(noticeId)}:retry`, { idempotency_key: key, confirm_duplicate_risk: confirmed })).data;
export const getAccountDetail = async (accountId: string): Promise<AccountDetail> =>
  (await axiosInstance.get(`${gateway}/channel-accounts/${id(accountId)}`)).data;
export const getTargets = async (accountId: string, cursor = '', recipientId = ''): Promise<Page<Target>> =>
  (await axiosInstance.get(`${gateway}/channel-accounts/${id(accountId)}/notification-targets`, { params: { cursor, limit: 100, ...(recipientId ? { recipient_id: recipientId } : {}) } })).data;
export const getReferences = async (accountId: string, cursor = ''): Promise<Page<Reference>> =>
  (await axiosInstance.get(`${gateway}/channel-accounts/${id(accountId)}/notification-references`, { params: { cursor, limit: 100 } })).data;
export function notificationError(error: unknown): { reason: string; running_task_ids?: string[] } {
  const data = (error as { response?: { data?: { data?: { detail?: { reason?: string; running_task_ids?: string[] } }; error?: { code?: string } } } })?.response?.data;
  return { ...data?.data?.detail, reason: data?.data?.detail?.reason || data?.error?.code || 'NOTIFICATION_UNAVAILABLE' };
}
export function emptyRule(): NotificationConfig {
  return { events: { succeeded: { enabled: true, content: 'summary' }, failed: { enabled: true, content: 'summary' }, waiting: { enabled: false, content: 'summary' } }, channels: { desktop: { enabled: false } } };
}
export function ruleError(config: NotificationConfig, defaults = false): string | undefined {
  if ((defaults || Object.values(config.channels).some(c => c?.enabled)) && !events.some(e => config.events[e].enabled)) return 'NOTIFICATION_EVENT_REQUIRED';
  if (!defaults && providers.some(p => config.channels[p]?.enabled && (!config.channels[p]?.account_id || !config.channels[p]?.recipient_id))) return 'NOTIFICATION_TARGET_REQUIRED';
}

export const getGroups = listNotificationGroups;
export const setDefaultRecipient = updateDefaultRecipient;
