import { useCallback, useEffect, useRef, useState } from 'react';
import { dataSourceCloudOauthApi } from '@/modules/dataSource/api/clients';
import { getCloudConnectionItems } from '@/modules/dataSource/mappers/cloudConnection';

const cloudProviders = new Set(['feishu', 'github', 'googledrive', 'notion', 'wechat']);
export type ProviderAvailability = 'checking' | 'ready' | 'authorize' | 'chat-disabled' | 'failed';

export function useWriterProviderAvailability(providers: string[]) {
  const [states, setStates] = useState<Record<string, ProviderAvailability>>({});
  const generation = useRef(0);
  const signature = providers.join(',');
  const refresh = useCallback(async () => {
    const ids = signature.split(',').filter(Boolean);
    const request = ++generation.current;
    setStates(Object.fromEntries(ids.map(id => [id, cloudProviders.has(id) ? 'checking' : 'ready'])));
    const results = await Promise.all(ids.filter(id => cloudProviders.has(id)).map(async id => {
      try {
        const response = await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({ provider: id, status: null });
        const connections = getCloudConnectionItems(response.data);
        if (connections.some(connection => typeof connection.can_use_chat !== 'boolean')) {
          return [id, 'failed'] as const;
        }
        const ready = connections.some(connection => connection.can_use_chat === true);
        const chatDisabled = connections.some(connection => {
          if (connection.status !== 'ACTIVE') return false;
          const options = connection.provider_options ?? {};
          const meta = connection.provider_account_meta ?? {};
          return (options.chat_enabled ?? options.chatEnabled ?? meta.chat_enabled ?? meta.chatEnabled) === false;
        });
        return [id, ready ? 'ready' : id === 'feishu' && chatDisabled ? 'chat-disabled' : 'authorize'] as const;
      } catch { return [id, 'failed'] as const; }
    }));
    if (request === generation.current) setStates(current => ({ ...current, ...Object.fromEntries(results) }));
    return Object.fromEntries(results);
  }, [signature]);
  useEffect(() => {
    void refresh();
    const onFocus = () => void refresh();
    window.addEventListener('focus', onFocus);
    return () => { generation.current++; window.removeEventListener('focus', onFocus); };
  }, [refresh]);
  return { states, refresh };
}
