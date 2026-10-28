import { useCallback, useEffect, useRef, useState } from 'react';

import { axiosInstance, BASE_URL } from '@/components/request';
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT, AUTH_LOGOUT_EVENT } from '@/components/auth';

export interface PaperNotification {
  id: string;
  title: string;
  message: string;
  level?: 'info' | 'success' | 'warning' | 'error';
  importBatchId?: string;
  action?: { label: string; onClick: () => void | Promise<void>; disabled?: boolean; closeOnClick?: boolean };
}

export function usePaperNotifications(datasetId: string) {
  const scope = useRef(datasetId);
  scope.current = datasetId;
  const [state, setState] = useState<{ datasetId: string; items: PaperNotification[] }>({ datasetId, items: [] });
  const notify = useCallback((notice: PaperNotification) => {
    if (scope.current !== datasetId) return;
    setState(current => ({ datasetId, items: [...(current.datasetId === datasetId ? current.items : []).filter(item => item.id !== notice.id), notice] }));
  }, [datasetId]);
  useEffect(() => {
    let stopped = false; let inFlight = false; let generation = 0;
    const refresh = async () => {
      const user = AgentAppsAuth.getUserInfo()?.userId;
      if (!user || stopped || inFlight) return;
      inFlight = true; const revision = generation;
      try {
        const response = await axiosInstance.get(`${BASE_URL}/api/core/academic/notifications`, { silentError: true } as never);
        const data = response.data?.data || response.data;
        if (stopped || revision !== generation || scope.current !== datasetId || AgentAppsAuth.getUserInfo()?.userId !== user) return;
        const notices = (data.items || []).filter((item: { navigation?: { dataset_id?: string } }) => item.navigation?.dataset_id === datasetId);
        const items: PaperNotification[] = notices.map((item: { notification_id: string; event: string; title: string; body: string; navigation: { expansion_id: string } }) => ({
          id: `persistent:${item.notification_id}`, title: item.title, message: item.body,
          level: item.event === 'paper_ready' ? 'success' : 'info',
          action: { label: '打开拓展记录', closeOnClick: true, onClick: () => { window.dispatchEvent(new CustomEvent('paper-expansion-open', { detail: { id: item.navigation.expansion_id } })); } },
        }));
        setState(current => ({ datasetId, items: [...(current.datasetId === datasetId ? current.items.filter(item => !item.id.startsWith('persistent:')) : []), ...items] }));
      } catch { /* Persisted pending items are fetched on the next refresh. */ }
      finally { inFlight = false; }
    };
    const invalidate = () => { generation += 1; setState({ datasetId, items: [] }); void refresh(); };
    const focus = () => { void refresh(); };
    void refresh(); const timer = window.setInterval(focus, 10000);
    window.addEventListener('focus', focus); window.addEventListener(AUTH_USER_CHANGE_EVENT, invalidate); window.addEventListener(AUTH_LOGOUT_EVENT, invalidate);
    return () => { stopped = true; clearInterval(timer); window.removeEventListener('focus', focus); window.removeEventListener(AUTH_USER_CHANGE_EVENT, invalidate); window.removeEventListener(AUTH_LOGOUT_EVENT, invalidate); };
  }, [datasetId]);
  const resolve = useCallback((id: string) => {
    if (scope.current !== datasetId) return;
    if (id.startsWith('persistent:')) void axiosInstance.post(`${BASE_URL}/api/core/academic/notifications/${encodeURIComponent(id.slice(11))}:read`, {}, { silentError: true } as never).catch(() => {});
    setState(current => current.datasetId === datasetId && current.items.some(item => item.id === id)
      ? { datasetId, items: current.items.filter(item => item.id !== id) } : current);
  }, [datasetId]);
  return { notifications: state.datasetId === datasetId ? state.items : [], notify, resolve };
}
