import type { TabDef, WorkflowSession } from '@/modules/chat/store/workflowPanel';

export function resolveCompletedWriterContinueStep(
  session: Pick<WorkflowSession, 'status' | 'workflow_id'>,
  activeTab?: TabDef,
): string | undefined {
  if (
    session.status === 'completed'
    && session.workflow_id === 'writer-workflow'
    && (activeTab?.step_id ?? activeTab?.id) === 'outline'
  ) {
    return 'write_document';
  }
  return undefined;
}
