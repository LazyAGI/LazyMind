import { useCallback, useState } from 'react';
import type { SlotFooterAction } from './slotEditingContext';

export function useSlotFooterActions() {
  const [footerActions, setFooterActions] = useState<Map<string, SlotFooterAction>>(new Map());
  const registerFooterAction = useCallback((key: string, action: SlotFooterAction | null) => {
    setFooterActions(previous => {
      const next = new Map(previous);
      if (action) next.set(key, action);
      else next.delete(key);
      return next;
    });
    return () => setFooterActions(previous => {
      if (previous.get(key) !== action) return previous;
      const next = new Map(previous);
      next.delete(key);
      return next;
    });
  }, []);
  const clearFooterActions = useCallback(() => setFooterActions(new Map()), []);

  return { footerActions, registerFooterAction, clearFooterActions };
}
