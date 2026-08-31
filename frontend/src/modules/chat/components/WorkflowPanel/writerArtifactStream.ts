import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import type { SubAgentTask, TaskArtifactStream } from '@/modules/chat/store/taskCenter';

const WRITER_STREAM_SLOT_IDS = new Set([
  'outline_document',
  'flat_draft_document',
  'draft_document',
]);

export function findWriterArtifactStream(
  session: WorkflowSession,
  stepId: string | undefined,
  slotId: string,
  tasks: SubAgentTask[],
): TaskArtifactStream | undefined {
  if (!WRITER_STREAM_SLOT_IDS.has(slotId) || !stepId) return undefined;

  const findStream = (task: SubAgentTask | undefined) => task?.artifact_streams
    ?.slice()
    .reverse()
    .find((candidate) => candidate.slot === slotId && candidate.content_type === 'text/markdown');

  const steps = (session.steps ?? [])
    .filter((step) => step.step_id === stepId)
    .slice()
    .reverse();
  for (const step of steps) {
    const stream = findStream(tasks.find((task) => task.task_id === step.task_id));
    if (stream) return stream;
  }

  // The session projection can briefly lag task_created. Only use the fallback
  // when one live stream for this exact slot is unambiguous.
  const liveStreams = tasks
    .filter((task) => task.status === 'pending' || task.status === 'running')
    .map(findStream)
    .filter((stream): stream is TaskArtifactStream => Boolean(stream));
  return liveStreams.length === 1 ? liveStreams[0] : undefined;
}
