import { AgentAppsAuth } from '@/components/auth';
import { listChannelAccounts, type ChannelAccount } from '@/modules/channelGateway/api';
import { getTargets } from './api';
import { wechatAccountStatus, wechatTargetStatus, type WeChatNotificationStatus } from './wechatNotificationStatus';

export const WECHAT_NOTIFICATION_STATUS_CHANGED_EVENT = 'lazymind:wechat-notification-status-changed';
export { WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT } from './notificationEvents';
export interface WeChatNotificationTarget { account_id: string; recipient_id: string }
export interface CheckedWeChatTarget extends WeChatNotificationTarget { status: WeChatNotificationStatus; account?: ChannelAccount }
let identity = '';
let generation = 0;
let version = 0;
const states = new Map<string, CheckedWeChatTarget>();
const active = new Map<string, number>();
const inflight = new Map<string, Promise<CheckedWeChatTarget | undefined>>();
let configured: WeChatNotificationTarget[] = [];
const key = (target: WeChatNotificationTarget) => JSON.stringify([target.account_id, target.recipient_id]);
const emit = () => { version += 1; window.dispatchEvent(new Event(WECHAT_NOTIFICATION_STATUS_CHANGED_EVENT)); };
function currentIdentity() { return AgentAppsAuth.getSessionIdentity?.() || JSON.stringify(AgentAppsAuth.getUserInfo()); }
function ensureIdentity() {
  const current = currentIdentity();
  if (current !== identity) { identity = current; generation += 1; states.clear(); active.clear(); inflight.clear(); configured = []; version += 1; }
}
export function clearWeChatNotificationState() {
  identity = currentIdentity(); generation += 1; states.clear(); active.clear(); inflight.clear(); configured = []; emit();
}
export function subscribeWeChatNotificationState(listener: () => void) {
  window.addEventListener(WECHAT_NOTIFICATION_STATUS_CHANGED_EVENT, listener);
  return () => window.removeEventListener(WECHAT_NOTIFICATION_STATUS_CHANGED_EVENT, listener);
}
export function wechatNotificationStateVersion() { ensureIdentity(); return version; }
export function setConfiguredWeChatTargets(targets: WeChatNotificationTarget[]) {
  ensureIdentity();
  const unique = [...new Map(targets.filter(target => target.account_id && target.recipient_id).map(target => [key(target), target])).values()];
  if (JSON.stringify(unique) !== JSON.stringify(configured)) { configured = unique; emit(); }
}
export function registerWeChatTarget(target: WeChatNotificationTarget) {
  ensureIdentity();
  const targetKey = key(target), captured = generation;
  active.set(targetKey, (active.get(targetKey) || 0) + 1); emit();
  return () => {
    if (captured !== generation) return;
    const count = (active.get(targetKey) || 1) - 1;
    if (count) active.set(targetKey, count); else active.delete(targetKey);
    emit();
  };
}
export function readWeChatTarget(target: WeChatNotificationTarget) { ensureIdentity(); return states.get(key(target)); }
export function hasConfiguredWeChatTarget(target: WeChatNotificationTarget) {
  ensureIdentity(); return configured.some(value => key(value) === key(target));
}
export function visibleWeChatTargets() {
  ensureIdentity();
  const keys = new Set([...configured.map(key), ...active.keys()]);
  return [...states].filter(([targetKey]) => keys.has(targetKey)).map(([, value]) => value);
}

// Checking is possible only after a caller has selected a specific recipient.
// The gateway verifies ownership and that this is an existing recipient.
export function checkWeChatNotificationTarget(target: WeChatNotificationTarget): Promise<CheckedWeChatTarget | undefined> {
  ensureIdentity();
  if (!target.account_id || !target.recipient_id) return Promise.resolve(undefined);
  const targetKey = key(target), captured = generation, session = identity;
  const pending = inflight.get(targetKey);
  if (pending) return pending;
  if (!states.has(targetKey)) { states.set(targetKey, { ...target, status: 'checking' }); emit(); }
  const current = () => captured === generation && currentIdentity() === session;
  const request = (async () => {
    let account: ChannelAccount | undefined;
    let status: WeChatNotificationStatus = 'unavailable';
    try {
      const targets = await getTargets(target.account_id, '', target.recipient_id, { checkConnection: true });
      if (!current()) return;
      const accounts = await listChannelAccounts('wechat', { silentError: true });
      if (!current()) return;
      account = accounts.items.find(value => value.id === target.account_id && value.provider === 'wechat');
      const recipient = targets.items.find(value => value.recipient_id === target.recipient_id);
      status = wechatTargetStatus(account, recipient);
    } catch (error) {
      const code = (error as { response?: { data?: { error?: { code?: string } } } }).response?.data?.error?.code;
      if (code === 'NOTIFICATION_TARGET_UNAVAILABLE') {
        status = 'targetUnavailable';
        // This rejection can also mean the account is disconnected. Read its
        // cached state to offer the right repair without another provider probe.
        try {
          const accounts = await listChannelAccounts('wechat', { silentError: true });
          if (!current()) return;
          account = accounts.items.find(value => value.id === target.account_id && value.provider === 'wechat');
          const accountStatus = wechatAccountStatus(account);
          if (accountStatus === 'disconnected' || accountStatus === 'reauthorization') status = accountStatus;
        } catch { /* Retain the known target failure if account metadata is unavailable. */ }
      }
      if (code === 'ACCOUNT_NOT_FOUND') status = 'disconnected';
    }
    if (!current()) return;
    const result = { ...target, account, status };
    states.set(targetKey, result); emit(); return result;
  })().finally(() => { if (captured === generation) inflight.delete(targetKey); });
  inflight.set(targetKey, request);
  return request;
}
