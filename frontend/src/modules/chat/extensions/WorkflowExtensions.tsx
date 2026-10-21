import type { ComponentType } from 'react';
import type { WorkflowSession } from '../store/workflowPanel';
import { ProductProject } from '../components/WorkflowPanel/ProductProject';

export interface WorkflowExtensionProps {
  session: WorkflowSession; disabled: boolean; beforeAction: () => Promise<boolean>;
  onRefresh: () => void; onSendMessage?: (message: string) => void;
}
// Installed capability implementations. A package opts in explicitly; no workflow IDs.
export const installedWorkflowExtensions: Record<string, ComponentType<WorkflowExtensionProps>> = {
  'product-project-v1': ProductProject,
};
export function WorkflowExtensions({ names, ...props }: WorkflowExtensionProps & { names?: string[] }) {
  return <>{[...new Set(names ?? [])].map(name => {
    const Component = installedWorkflowExtensions[name];
    return Component ? <Component key={`${props.session.session_id}:${name}`} {...props} />
      : <div role="alert" key={name}>Unavailable workflow extension: {name}</div>;
  })}</>;
}
