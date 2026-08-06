import { useCallback, useEffect, useState } from 'react';

/** `${sessionId}:${slotId}:${listIndex}` */
export type PopoverKey = string;

let openPopoverKey: PopoverKey | null = null;
const popoverListeners = new Set<() => void>();

function notifyPopoverListeners() {
  popoverListeners.forEach((fn) => fn());
}

/** Keeps at most one version popover open across all slots. */
export function useGlobalPopoverOpen(key: PopoverKey): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const listener = () => {
      setOpen(openPopoverKey === key);
    };
    popoverListeners.add(listener);
    return () => { popoverListeners.delete(listener); };
  }, [key]);

  const setGlobalOpen = useCallback((next: boolean) => {
    if (next) {
      openPopoverKey = key;
    } else if (openPopoverKey === key) {
      openPopoverKey = null;
    }
    notifyPopoverListeners();
  }, [key]);

  return [open, setGlobalOpen];
}
