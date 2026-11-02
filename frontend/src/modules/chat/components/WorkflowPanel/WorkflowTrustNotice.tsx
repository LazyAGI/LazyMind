import { useEffect, useRef, useState } from 'react';
import { CloseOutlined, InfoCircleOutlined, LoadingOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import {
  fetchUserUiPreferences,
  patchUserUiPreferences,
  USER_UI_PREFERENCES_CHANGED_EVENT,
} from '@/modules/user/uiPreferencesApi';

export function WorkflowTrustNotice() {
  const { t } = useTranslation();
  const [dismissed, setDismissed] = useState<boolean | null>(null);
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const pending = useRef(false);

  useEffect(() => {
    let disposed = false;
    let sequence = 0;
    const load = async () => {
      const request = ++sequence;
      setDismissed(null);
      setFailed(false);
      try {
        const prefs = await fetchUserUiPreferences({ silentError: true } as never);
        if (!disposed && request === sequence) setDismissed(prefs.workflow_trust_notice_dismissed === true);
      } catch {
        if (!disposed && request === sequence) setDismissed(false);
      }
    };
    const changed = (event: Event) => {
      const value = (event as CustomEvent).detail?.workflow_trust_notice_dismissed;
      if (typeof value === 'boolean') {
        sequence++;
        setDismissed(value);
      }
    };
    void load();
    window.addEventListener(USER_UI_PREFERENCES_CHANGED_EVENT, changed);
    window.addEventListener(AUTH_USER_CHANGE_EVENT, load);
    return () => {
      disposed = true;
      window.removeEventListener(USER_UI_PREFERENCES_CHANGED_EVENT, changed);
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, load);
    };
  }, []);

  const dismiss = async () => {
    if (pending.current) return;
    pending.current = true;
    setSaving(true);
    setFailed(false);
    try {
      const prefs = await patchUserUiPreferences({ workflow_trust_notice_dismissed: true });
      if (prefs.workflow_trust_notice_dismissed !== true) throw new Error('Preference not saved');
      setDismissed(true);
    } catch {
      setFailed(true);
    } finally {
      pending.current = false;
      setSaving(false);
    }
  };

  if (dismissed !== false) return null;
  return <div className='workflow-panel__trust-notice' role='note'>
    <InfoCircleOutlined aria-hidden='true' />
    <div className='workflow-panel__trust-notice-content'>
      <span>{t('chat.workflowFullTrustNotice')}</span>
      {failed && <div role='alert'>{t('chat.workflowTrustNoticeDismissFailed')}</div>}
    </div>
    <button type='button' className='workflow-panel__trust-notice-close'
      title={t('chat.workflowTrustNoticeDismiss')} aria-label={t('chat.workflowTrustNoticeDismiss')}
      disabled={saving} onClick={() => void dismiss()}>
      {saving ? <LoadingOutlined aria-hidden='true' /> : <CloseOutlined aria-hidden='true' />}
    </button>
  </div>;
}
