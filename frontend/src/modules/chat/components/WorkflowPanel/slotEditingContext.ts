import { createContext } from 'react';

/**
 * Context for slot edit lifecycle:
 * - setEditing: track dirty/in-progress editors (dismiss stays blocked)
 * - registerFlush: let footer actions flush pending saves before retry/continue
 */
export interface SlotEditingContextValue {
  setEditing: (key: string, editing: boolean) => void;
  registerFlush: (key: string, flush: () => Promise<boolean>) => () => void;
}

export const SlotEditingContext = createContext<SlotEditingContextValue>({
  setEditing: () => {},
  registerFlush: () => () => {},
});
