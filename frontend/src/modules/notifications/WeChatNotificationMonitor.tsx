import { useEffect, useState, useSyncExternalStore } from 'react';
import { Alert } from 'antd';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AgentAppsAuth } from '@/components/auth';
import { WECHAT_CONNECTION_CHECK_INTERVAL_MS } from '@/modules/channelGateway/api';
import { channelGateEnabled, getConfiguredWechatTargets, getPreferences, NOTIFICATION_PREFERENCES_CHANGED_EVENT } from './api';
import { checkWeChatNotificationTarget, setConfiguredWeChatTargets, subscribeWeChatNotificationState,
  visibleWeChatTargets, wechatNotificationStateVersion } from './wechatNotificationState';
import { WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT, WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT, subscribeNotificationChange } from './notificationEvents';
import { wechatStatusHint, type WeChatNotificationStatus } from './wechatNotificationStatus';

// Settings only starts probes for already-configured task recipients. Checks
// initiated by confirming a recipient publish to the same state as this banner.
export default function WeChatNotificationMonitor({ userKey }: { userKey: string }) {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(false);
  useSyncExternalStore(subscribeWeChatNotificationState, wechatNotificationStateVersion);
  useEffect(() => {
    if (!userKey) return;
    const session = AgentAppsAuth.getSessionIdentity();
    let active = true, generation = 0, inFlight = false, pending = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    setEnabled(false);
    const check = async () => {
      if (!active || inFlight || session !== AgentAppsAuth.getSessionIdentity()) return;
      clearTimeout(timer); inFlight = true;
      const version = generation;
      const current = () => active && version === generation && session === AgentAppsAuth.getSessionIdentity();
      try {
        const preferences = await getPreferences({ silentError: true });
        if (!current()) return;
        const allowed = channelGateEnabled(preferences, 'wechat');
        setEnabled(allowed);
        if (!allowed) { setConfiguredWeChatTargets([]); return; }
        const result = await getConfiguredWechatTargets({ silentError: true });
        if (!current()) return;
        const targets = result.items.filter(target => target.account_id && target.recipient_id);
        setConfiguredWeChatTargets(targets);
        for (let start = 0; start < targets.length && current(); start += 4) {
          await Promise.allSettled(targets.slice(start, start + 4).map(checkWeChatNotificationTarget));
        }
      } catch { /* Discovery errors do not prove a WeChat conversation expired. */ }
      finally {
        inFlight = false;
        if (active) {
          if (pending) { pending = false; void check(); }
          else timer = setTimeout(() => { void check(); }, WECHAT_CONNECTION_CHECK_INTERVAL_MS);
        }
      }
    };
    const refresh = () => {
      generation += 1; clearTimeout(timer);
      if (inFlight) pending = true; else void check();
    };
    const preferencesChanged = () => { setEnabled(false); refresh(); };
    const unsubscribePreferences = subscribeNotificationChange(NOTIFICATION_PREFERENCES_CHANGED_EVENT, preferencesChanged);
    const unsubscribeConfig = subscribeNotificationChange(WECHAT_NOTIFICATION_CONFIG_CHANGED_EVENT, refresh);
    window.addEventListener(WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT, refresh);
    window.addEventListener('focus', refresh);
    void check();
    return () => {
      active = false; clearTimeout(timer);
      unsubscribePreferences(); unsubscribeConfig();
      window.removeEventListener(WECHAT_NOTIFICATION_CHECK_REQUESTED_EVENT, refresh);
      window.removeEventListener('focus', refresh);
    };
  }, [userKey]);
  const issues = enabled ? visibleWeChatTargets().filter(value => !['connected', 'checking', 'unconfigured'].includes(value.status)) : [];
  if (!issues.length) return null;
  const kinds = [...new Set(issues.map(value => value.status))];
  const hint = (status: WeChatNotificationStatus) => status === 'unavailable' ? 'wechatConnectionCheckFailed' : wechatStatusHint(status);
  return <Alert type="warning" showIcon className="wechat-notification-monitor" style={{ flexShrink: 0, marginBottom: 8 }}
    message={t('notifications.' + (issues.every(value => value.status === 'pending') ? 'wechatActivationRequired' : 'wechatConnectionIssue'))}
    description={<>{kinds.map(status => <p key={status} style={{ margin: '4px 0' }}>
      {t('notifications.' + hint(status))} {t('notifications.wechatAffectedAccounts', {
        accounts: issues.filter(value => value.status === status).map(value => value.account?.label || value.account_id).join('、'),
      })}
    </p>)}</>}
    action={<Link to="/settings?section=notifications">{t('notifications.wechatOpenSettings')}</Link>}
  />;
}
