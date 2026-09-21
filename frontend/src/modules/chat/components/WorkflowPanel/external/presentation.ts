import { createContext } from 'react';
import type { ExecutionActivity } from './useExecutionActivity';

/** Supplied only by the external run page. No subscriptions in the shared panel. */
export interface ExternalWorkflowPresentation {
  expanded?: boolean;
  onToggleExpand?: () => void;
  collapsed?: boolean;
  onToggleCollapse?: () => void;
  activities: Record<string, ExecutionActivity>;
}
export const ExternalWorkflowPresentationContext = createContext(false);
