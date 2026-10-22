import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react';
import { checkWeChatNotificationTarget, hasConfiguredWeChatTarget, readWeChatTarget, registerWeChatTarget, subscribeWeChatNotificationState,
  visibleWeChatTargets, wechatNotificationStateVersion } from './wechatNotificationState';
import { WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT } from './notificationEvents';

export default function useWeChatNotificationStatus({ enabled, ready = true, accountId, recipientId, refreshKey = 0, aggregate = false }: {
  enabled: boolean; ready?: boolean; accountId?: string; recipientId?: string; refreshKey?: string | number; aggregate?: boolean;
}) {
  useSyncExternalStore(subscribeWeChatNotificationState, wechatNotificationStateVersion);
  const confirmed = Boolean(enabled && accountId && recipientId);
  const checkedSelection = useRef<string>();
  const previousRefreshKey = useRef(refreshKey);
  const refresh = useCallback(() => {
    if (enabled && (confirmed || aggregate)) window.dispatchEvent(new Event(WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT));
  }, [confirmed, enabled, aggregate]);
  useEffect(() => {
    const requested = previousRefreshKey.current !== refreshKey;
    previousRefreshKey.current = refreshKey;
    // A settings reload requests checks through the saved-target catalog. A
    // settings mount or switch change alone must never choose a recipient.
    if (requested && enabled) refresh();
  }, [refreshKey, enabled, aggregate, refresh]);
  useEffect(() => {
    if (!confirmed) { checkedSelection.current = undefined; return; }
    if (!ready) return;
    const selection = JSON.stringify([accountId, recipientId]);
    if (checkedSelection.current === selection) return;
    checkedSelection.current = selection;
    const target = { account_id: accountId!, recipient_id: recipientId! };
    // The saved-target monitor may have finished while account metadata loaded.
    if (!hasConfiguredWeChatTarget(target) || !readWeChatTarget(target)) void checkWeChatNotificationTarget(target);
  }, [confirmed, ready, accountId, recipientId]);
  useEffect(() => {
    if (!confirmed || !ready) return;
    const release = registerWeChatTarget({ account_id: accountId!, recipient_id: recipientId! });
    // Periodic/focus checks belong to the monitor's saved-target catalog.
    // An editor selection can be checked immediately without becoming saved.
    return release;
  }, [confirmed, ready, accountId, recipientId]);
  const snapshots = enabled ? visibleWeChatTargets() : [];
  const snapshot = confirmed ? readWeChatTarget({ account_id: accountId!, recipient_id: recipientId! })
    : aggregate ? snapshots.find(value => value.status !== 'connected' && value.status !== 'checking') || snapshots[0] : undefined;
  return { status: enabled ? snapshot?.status ?? 'unconfigured' as const : undefined,
    account: snapshot?.account, refresh };
}
