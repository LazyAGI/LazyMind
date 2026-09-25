import { createContext } from 'react';
import type { ExecutionActivity } from './useExecutionActivity';

/** Supplied only by the external run page. No subscriptions in the shared panel. */
export interface ExternalWorkflowPresentation {
  /** Optional DSH empty-state presentation; execution activity does not change shared slot behavior. */
  compactEmptyStates?: boolean;
  expanded?: boolean;
  onToggleExpand?: () => void;
  collapsed?: boolean;
  onToggleCollapse?: () => void;
  activities: Record<string, ExecutionActivity>;
}
export const CompactWorkflowEmptyStatesContext = createContext(false);
