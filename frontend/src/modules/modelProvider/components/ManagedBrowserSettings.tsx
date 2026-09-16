import { useEffect, useState, type ChangeEvent } from 'react';
import { Alert, Button, Input, Select, Space, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { managedBrowserStatus, openManagedBrowser, selectManagedBrowser, type ManagedBrowserStatus } from '@/runtime/managedBrowser';

export default function ManagedBrowserSettings() {
  const { t } = useTranslation();
  const [url, setURL] = useState('https://github.com');
  const [status, setStatus] = useState<ManagedBrowserStatus>({ state: 'connecting' });
  const [error, setError] = useState('');
  const [opening, setOpening] = useState(false);
  const [switching, setSwitching] = useState(false);
  useEffect(() => {
    let active = true;
    const refresh = () => { void managedBrowserStatus().then(value => {
      if (active) setStatus(value);
    }).catch(() => { if (active) setStatus({ state: 'error' }); }); };
    refresh();
    const timer = window.setInterval(refresh, 2000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);
  const open = async () => {
    setOpening(true); setError('');
    try { await openManagedBrowser(url); }
    catch (value) { setError(value instanceof Error ? value.message : String(value)); }
    finally { setOpening(false); }
  };
  const select = async (engine: 'builtin' | 'edge') => {
    setSwitching(true); setError('');
    try { setStatus(await selectManagedBrowser(engine)); }
    catch (value) { setError(value instanceof Error ? value.message : String(value)); }
    finally { setSwitching(false); }
  };
  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    <Alert type="info" showIcon message={t('modelProvider.external.managedBrowserSummary')} />
    <Select aria-label={t('modelProvider.external.managedBrowserChoice')} style={{ width: '100%' }}
      value={status.engine || 'builtin'} loading={switching} disabled={switching || opening}
      onChange={select} options={[
        { value: 'builtin', label: t('modelProvider.external.managedBrowserBuiltin') },
        { value: 'edge', label: status.edgeAvailable ? 'Microsoft Edge' : t('modelProvider.external.managedBrowserEdgeMissing'), disabled: !status.edgeAvailable },
      ]} />
    <p>{t('modelProvider.external.managedBrowserSwitch')}</p>
    <Tag color={status.state === 'connected' ? 'success' : 'default'}>
      {t(`modelProvider.external.managedBrowserState.${status.state}`)}
    </Tag>
    <p>{t('modelProvider.external.managedBrowserLogin')}</p>
    <Space.Compact style={{ width: '100%' }}>
      <Input disabled={switching} aria-label={t('modelProvider.external.managedBrowserURL')} value={url}
        onChange={(event: ChangeEvent<HTMLInputElement>) => setURL(event.target.value)} onPressEnter={() => void open()} />
      <Button type="primary" disabled={switching} loading={opening} onClick={() => void open()}>
        {t('modelProvider.external.managedBrowserOpen')}
      </Button>
    </Space.Compact>
    {error || status.error ? <Alert type="error" showIcon message={error || status.error} /> : null}
  </Space>;
}
