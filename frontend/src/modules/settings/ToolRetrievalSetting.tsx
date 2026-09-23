import { useEffect, useState } from 'react';
import { Alert, Switch } from 'antd';
import { useTranslation } from 'react-i18next';
import { ConversationSettingsApi } from '@/modules/chat/utils/request';

export default function ToolRetrievalSetting() {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    ConversationSettingsApi().getChatSettings({ signal: controller.signal }).then(response => {
      if (controller.signal.aborted) return;
      const payload = response.data as typeof response.data & { data?: typeof response.data };
      setEnabled((payload.data ?? payload).enable_tool_retrieval === true);
      setReady(true);
    }).catch(() => {
      if (!controller.signal.aborted) setError(true);
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, []);
  const save = async (value: boolean) => {
    setLoading(true);
    setError(false);
    try {
      await ConversationSettingsApi().setToolRetrieval(value);
      setEnabled(value);
    } catch {
      setError(true);
    } finally {
      setLoading(false);
    }
  };
  return <section className="settings-entry-defaults" aria-label={t('toolRetrieval.title')}>
    <div className="settings-entry-defaults-row">
      <div><strong>{t('toolRetrieval.title')}</strong><p>{t('toolRetrieval.description')}</p></div>
      <Switch className="settings-ref-switch" aria-label={t('toolRetrieval.title')} checked={enabled} loading={loading}
        disabled={!ready || loading} onChange={save} />
    </div>
    {error && <Alert type="error" message={t('toolRetrieval.error')} />}
  </section>;
}
