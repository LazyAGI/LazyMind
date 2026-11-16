import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Radio, Space, Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import { getLocalizedErrorMessage } from '@/components/request';
import { modelProvidersApi, unwrapModelProviderData } from '../api';

type Group = { group_id: string; provider_name: string; group_name: string };

export default function ParsingServiceSelection({ revision, onChanged }: {
  revision: number; onChanged?: () => void;
}) {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<Group[]>([]);
  const [selected, setSelected] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  const generation = useRef(0);
  const pending = useRef(false);
  useEffect(() => {
    const current = ++generation.current;
    setLoading(true);
    setError('');
    Promise.resolve().then(() => Promise.all([
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({ category: 'ocr' }),
      modelProvidersApi.apiCoreModelProvidersSelectedProvidersGet(),
    ])).then(([available, active]) => {
      if (generation.current !== current) return;
      setGroups(unwrapModelProviderData<{ groups?: Group[] }>(available.data).groups || []);
      const selections = unwrapModelProviderData<{ selections?: { category: string; group_id: string }[] }>(active.data).selections || [];
      setSelected(selections.find(item => item.category === 'ocr')?.group_id || '');
    }).catch(cause => {
      if (generation.current === current) { setGroups([]); setError(getLocalizedErrorMessage(cause)); }
    }).finally(() => { if (generation.current === current) setLoading(false); });
    return () => { generation.current++; };
  }, [revision, retry]);

  const select = async (groupId: string) => {
    if (pending.current || loading || groupId === selected) return;
    pending.current = true;
    setSaving(true);
    setError('');
    const current = generation.current;
    try {
      await modelProvidersApi.apiCoreModelProvidersSelectedProvidersPut({
        setSelectedProviderOpenAPIRequest: { selections: [{ category: 'ocr', group_id: groupId }] },
      });
      if (generation.current === current) { setSelected(groupId); onChanged?.(); }
    } catch (cause) {
      if (generation.current === current) setError(getLocalizedErrorMessage(cause));
    } finally { pending.current = false; setSaving(false); }
  };

  return <section aria-label={t('modelProvider.external.activeParser')} style={{ marginBottom: 16 }}>
    <Space direction="vertical" style={{ width: '100%' }}>
      <strong>{t('modelProvider.external.activeParser')}</strong>
      <span>{t('modelProvider.external.activeParserHint')}</span>
      {error && <Alert type="error" showIcon message={error} action={<Button onClick={() => setRetry(value => value + 1)} disabled={saving}>{t('common.retry')}</Button>} />}
      {loading ? <Spin size="small" /> : groups.length ? <Radio.Group
        aria-label={t('modelProvider.external.activeParser')}
        value={selected} disabled={saving} onChange={(event: { target: { value: string } }) => void select(event.target.value)}
      ><Space wrap>{groups.map(group => <Radio key={group.group_id} value={group.group_id}>
        {group.group_name && group.group_name !== group.provider_name ? `${group.provider_name} / ${group.group_name}` : group.provider_name}
      </Radio>)}</Space></Radio.Group> : !error && <span>{t('modelProvider.external.parserSetupRequired')}</span>}
      {saving && <Spin size="small" />}
    </Space>
  </section>;
}
