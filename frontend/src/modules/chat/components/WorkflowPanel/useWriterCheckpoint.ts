import { useContext, useEffect, useRef, type RefObject } from 'react';
import { WorkflowPanelTabActiveContext } from './slotEditingContext';

// The working draft may already be persisted while its five-second edit group is open.
export function useWriterCheckpoint(enabled: boolean, value: unknown, changed: boolean, root: RefObject<HTMLElement>, flush: () => Promise<boolean>) {
  const active = useContext(WorkflowPanelTabActiveContext);
  const flushRef = useRef(flush);
  flushRef.current = flush;
  const timer = useRef<number>();
  useEffect(() => {
    if (!enabled || !changed) return;
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      timer.current = undefined;
      void flushRef.current();
    }, 5000);
  }, [enabled, value, changed]);
  useEffect(() => {
    if (!enabled) return;
    const node = root.current;
    const finish = () => {
      window.clearTimeout(timer.current);
      timer.current = undefined;
      void flushRef.current();
    };
    const onBlur = (event: FocusEvent) => {
      if (event.relatedTarget instanceof Node && !node?.contains(event.relatedTarget)) finish();
    };
    const onVisibility = () => { if (document.hidden) finish(); };
    node?.addEventListener('focusout', onBlur);
    document.addEventListener('visibilitychange', onVisibility);
    if (!active) finish();
    return () => {
      node?.removeEventListener('focusout', onBlur);
      document.removeEventListener('visibilitychange', onVisibility);
      if (timer.current !== undefined) finish();
    };
  }, [enabled, root, active]);
}
