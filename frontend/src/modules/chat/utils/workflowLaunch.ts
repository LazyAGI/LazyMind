/** A one-time selection request. This is never an instruction to submit a task. */
export interface WorkflowLaunch {
  workflowRef: string;
  name: string;
}

export function readWorkflowLaunch(state: unknown): WorkflowLaunch | undefined {
  const launch = (state as { workflowLaunch?: Partial<WorkflowLaunch> } | null)?.workflowLaunch;
  if (typeof launch?.workflowRef !== 'string' || !launch.workflowRef
    || typeof launch.name !== 'string' || !launch.name) return;
  return { workflowRef: launch.workflowRef, name: launch.name };
}
