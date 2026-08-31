import type {
  TabDef,
  WorkflowSession,
  WorkflowTabAction,
} from '@/modules/chat/store/workflowPanel';
import { resolveExportActionProvider } from './exporterRegistry';

interface WorkflowTabActionsProps {
  actions?: WorkflowTabAction[];
  tab: TabDef;
  session: WorkflowSession;
  rows: number[];
}

/**
 * Generic action dispatcher. WorkflowPanel only understands the action contract;
 * provider modules own their input mapping, capability checks, and UI.
 */
export function WorkflowTabActions({ actions, tab, session, rows }: WorkflowTabActionsProps) {
  if (!actions?.length) return null;

  return (
    <>
      {actions.map((action) => {
        if (action.type !== 'export') return null;
        const ProviderAction = resolveExportActionProvider(action.provider);
        if (!ProviderAction) return null;
        return (
          <ProviderAction
            key={action.id}
            action={action}
            tab={tab}
            session={session}
            rows={rows}
          />
        );
      })}
    </>
  );
}
