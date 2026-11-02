import { useCallback, useRef, useState } from 'react';

export interface PaperNotification {
  id: string;
  title: string;
  message: string;
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
  const resolve = useCallback((id: string) => {
    if (scope.current !== datasetId) return;
    setState(current => current.datasetId === datasetId && current.items.some(item => item.id === id)
      ? { datasetId, items: current.items.filter(item => item.id !== id) } : current);
  }, [datasetId]);
  return { notifications: state.datasetId === datasetId ? state.items : [], notify, resolve };
}
