import { createContext } from 'react';

/**
 * Context for slot edit lifecycle:
 * - setEditing: track dirty/in-progress editors (dismiss stays blocked)
 * - registerFlush: let footer actions flush pending saves before retry/continue
 * - registerFooterAction: surface document actions (download / write-back) in the shared panel footer
 */
export interface SlotFooterAction {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  /** Persist any pending editor changes before running this action. */
  flushBeforeAction?: boolean;
  /** Lower values render further left within the document action group. */
  order?: number;
  tone?: 'primary' | 'secondary';
  icon?: 'write-back' | 'download';
  statusText?: string;
  statusTone?: 'success' | 'error';
  statusLink?: {
    href: string;
    label: string;
  };
}

export interface SlotEditingContextValue {
  setEditing: (key: string, editing: boolean) => void;
  registerFlush: (key: string, flush: () => Promise<boolean>) => () => void;
  registerFooterAction: (key: string, action: SlotFooterAction | null) => () => void;
}

export const SlotEditingContext = createContext<SlotEditingContextValue>({
  setEditing: () => {},
  registerFlush: () => () => {},
  registerFooterAction: () => () => {},
});

/** True when this slot tree is inside the currently selected plugin tab panel. */
export const PluginPanelTabActiveContext = createContext(true);
